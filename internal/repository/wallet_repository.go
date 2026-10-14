package repository

import (
	"context"
	"errors"
	"fmt"

	"backend-challenge-go/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrWalletNotFound   = errors.New("carteira não encontrada")
	ErrInsufficientFund = errors.New("saldo insuficiente para realizar a operação")
	ErrDuplicateKey     = errors.New("operação duplicada com chave de idempotência já processada")
)

type WalletModel struct {
	ID       string
	PlayerID string
	Currency string
	Balance  int64
	Version  int
}

type WalletRepository struct {
	pool *pgxpool.Pool
}

// NewWalletRepository cria a instância do repositório recebendo a pool de conexões
func NewWalletRepository(pool *pgxpool.Pool) *WalletRepository {
	return &WalletRepository{pool: pool}
}

// CreateWallet insere uma nova carteira e, se houver saldo inicial, regista o crédito no ledger
func (r *WalletRepository) CreateWallet(ctx context.Context, playerID, currency string, initialBalance int64) (*WalletModel, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("erro ao iniciar transação: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Cria a carteira
	query := `
		INSERT INTO wallets (player_id, currency, balance, version)
		VALUES ($1, $2, $3, 1)
		RETURNING id, player_id, currency, balance, version
	`
	var w WalletModel
	err = tx.QueryRow(ctx, query, playerID, currency, initialBalance).Scan(
		&w.ID,
		&w.PlayerID,
		&w.Currency,
		&w.Balance,
		&w.Version,
	)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar carteira: %w", err)
	}

	// 2. Se o saldo inicial for positivo, cria o registo OPENING e o lançamento de crédito no ledger
	if initialBalance > 0 {
		var txID string
		openTxQuery := `
			INSERT INTO wager_transactions (
				idempotency_key, payload_hash, wallet_id, kind, amount, currency, status, balance_at_execution
			) VALUES ($1, $2, $3, 'OPENING', $4, $5, 'PROCESSED', $6)
			RETURNING id
		`
		idempotencyKey := fmt.Sprintf("opening-%s", w.ID)
		payloadHash := fmt.Sprintf("hash-%s", idempotencyKey)

		err = tx.QueryRow(ctx, openTxQuery, idempotencyKey, payloadHash, w.ID, initialBalance, currency, initialBalance).Scan(&txID)
		if err != nil {
			return nil, fmt.Errorf("erro ao criar transação de abertura: %w", err)
		}

		ledgerQuery := `
			INSERT INTO wallet_ledger (
				wallet_id, transaction_id, direction, amount, balance_before, balance_after
			) VALUES ($1, $2, 'CREDIT', $3, 0, $4)
		`
		_, err = tx.Exec(ctx, ledgerQuery, w.ID, txID, initialBalance, initialBalance)
		if err != nil {
			return nil, fmt.Errorf("erro ao criar lançamento de abertura no ledger: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("falha ao confirmar transação: %w", err)
	}

	return &w, nil
}

// ProcessBet debita um valor da carteira garantindo consistência com lock transacional
func (r *WalletRepository) ProcessBet(
	ctx context.Context,
	walletID string,
	money domain.Money,
	idempotencyKey string,
	providerID string,
	externalTxID string,
) (*WalletModel, error) {
	// 1. Inicia transação atómica
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("falha ao iniciar transação: %w", err)
	}
	// Garante rollback caso ocorra erro antes do Commit
	defer tx.Rollback(ctx)

	// 2. Lock na linha da carteira (SELECT FOR UPDATE)
	var currentBalance int64
	var currentVersion int
	var currency string

	lockQuery := `
		SELECT balance, version, currency 
		FROM wallets 
		WHERE id = $1 
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, lockQuery, walletID).Scan(&currentBalance, &currentVersion, &currency)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWalletNotFound
		}
		return nil, fmt.Errorf("falha ao bloquear carteira: %w", err)
	}

	if currency != money.Currency() {
		return nil, domain.ErrCurrencyMismatch
	}

	// 3. Regra de negócio: verificar se há saldo suficiente
	betAmount := money.Amount()
	if currentBalance < betAmount {
		return nil, ErrInsufficientFund
	}

	newBalance := currentBalance - betAmount
	newVersion := currentVersion + 1

	// 4. Registar a transação
	var transactionID string
	txInsertQuery := `
		INSERT INTO wager_transactions (
			provider_id, external_transaction_id, idempotency_key, payload_hash, 
			wallet_id, kind, amount, currency, status, balance_at_execution
		) VALUES ($1, $2, $3, $4, $5, 'BET', $6, $7, 'PROCESSED', $8)
		RETURNING id
	`
	// Usamos hash simples provisório para a chave
	payloadHash := fmt.Sprintf("hash-%s", idempotencyKey)

	err = tx.QueryRow(
		ctx, txInsertQuery,
		providerID, externalTxID, idempotencyKey, payloadHash,
		walletID, betAmount, currency, newBalance,
	).Scan(&transactionID)
	if err != nil {
		return nil, fmt.Errorf("falha ao gravar wager_transaction: %w", err)
	}

	// 5. Registar no Ledger (Append-only)
	ledgerQuery := `
		INSERT INTO wallet_ledger (
			wallet_id, transaction_id, direction, amount, balance_before, balance_after
		) VALUES ($1, $2, 'DEBIT', $3, $4, $5)
	`
	_, err = tx.Exec(ctx, ledgerQuery, walletID, transactionID, betAmount, currentBalance, newBalance)
	if err != nil {
		return nil, fmt.Errorf("falha ao registar ledger: %w", err)
	}

	// 6. Atualizar saldo da carteira
	updateWalletQuery := `
		UPDATE wallets 
		SET balance = $1, version = $2, updated_at = NOW() 
		WHERE id = $3
	`
	_, err = tx.Exec(ctx, updateWalletQuery, newBalance, newVersion, walletID)
	if err != nil {
		return nil, fmt.Errorf("falha ao atualizar saldo da carteira: %w", err)
	}

	// 7. Efetiva todas as alterações no disco
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("falha no commit: %w", err)
	}

	return &WalletModel{
		ID:       walletID,
		Balance:  newBalance,
		Currency: currency,
		Version:  newVersion,
	}, nil
}
// GetWalletByID busca a carteira pelo ID
func (r *WalletRepository) GetWalletByID(ctx context.Context, walletID string) (*WalletModel, error) {
	query := `
		SELECT id, player_id, currency, balance, version 
		FROM wallets 
		WHERE id = $1
	`
	var w WalletModel
	err := r.pool.QueryRow(ctx, query, walletID).Scan(
		&w.ID,
		&w.PlayerID,
		&w.Currency,
		&w.Balance,
		&w.Version,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWalletNotFound
		}
		return nil, fmt.Errorf("erro ao buscar carteira: %w", err)
	}
	return &w, nil
}

type ReconciliationResult struct {
	WalletID          string
	StoredBalance     int64
	CalculatedBalance int64
	Difference        int64
	Consistent        bool
	CheckedEntries    int
}

// Reconcile calcula a soma do ledger e compara com o saldo atual da carteira
func (r *WalletRepository) Reconcile(ctx context.Context, walletID string) (*ReconciliationResult, error) {
	wallet, err := r.GetWalletByID(ctx, walletID)
	if err != nil {
		return nil, err
	}

	// Soma todos os créditos e subtrai todos os débitos registrados no ledger
	query := `
		SELECT 
			COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount WHEN direction = 'DEBIT' THEN -amount ELSE 0 END), 0) as calculated_balance,
			COUNT(*) as total_entries
		FROM wallet_ledger
		WHERE wallet_id = $1
	`
	var calculatedBalance int64
	var totalEntries int

	err = r.pool.QueryRow(ctx, query, walletID).Scan(&calculatedBalance, &totalEntries)
	if err != nil {
		return nil, fmt.Errorf("erro ao calcular saldo do ledger: %w", err)
	}

	diff := wallet.Balance - calculatedBalance

	return &ReconciliationResult{
		WalletID:          wallet.ID,
		StoredBalance:     wallet.Balance,
		CalculatedBalance: calculatedBalance,
		Difference:        diff,
		Consistent:        diff == 0,
		CheckedEntries:    totalEntries,
	}, nil
}