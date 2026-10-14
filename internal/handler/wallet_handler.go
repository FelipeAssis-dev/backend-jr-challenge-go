package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"backend-challenge-go/internal/domain"
	"backend-challenge-go/internal/repository"
)

type WalletHandler struct {
	repo *repository.WalletRepository
}

func NewWalletHandler(repo *repository.WalletRepository) *WalletHandler {
	return &WalletHandler{repo: repo}
}

// DTOs de entrada e saída
type CreateWalletRequest struct {
	PlayerID       string       `json:"playerId"`
	InitialBalance domain.Money `json:"initialBalance"`
}

type WalletResponse struct {
	ID       string       `json:"id"`
	PlayerID string       `json:"playerId"`
	Balance  domain.Money `json:"balance"`
	Version  int          `json:"version"`
}

type BetRequest struct {
	ProviderID            string       `json:"providerId"`
	ExternalTransactionID string       `json:"externalTransactionId"`
	PlayerID              string       `json:"playerId"`
	WalletID              string       `json:"walletId"`
	RoundID               string       `json:"roundId"`
	GameID                string       `json:"gameId"`
	Kind                  string       `json:"kind"`
	Money                 domain.Money `json:"money"`
}

type BetResponse struct {
	TransactionID    string       `json:"transactionId"`
	Status           string       `json:"status"`
	Balance          domain.Money `json:"balance"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

func (h *WalletHandler) CreateWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	var req CreateWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Payload JSON inválido", http.StatusBadRequest)
		return
	}

	wallet, err := h.repo.CreateWallet(r.Context(), req.PlayerID, req.InitialBalance.Currency(), req.InitialBalance.Amount())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	balanceMoney, _ := domain.NewMoney(wallet.Balance, wallet.Currency)
	resp := WalletResponse{
		ID:       wallet.ID,
		PlayerID: wallet.PlayerID,
		Balance:  balanceMoney,
		Version:  wallet.Version,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func (h *WalletHandler) ProcessTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(w, "Header Idempotency-Key é obrigatório", http.StatusBadRequest)
		return
	}

	var req BetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	if req.Kind != "BET" {
		http.Error(w, "Apenas BET suportado neste endpoint de demonstração", http.StatusBadRequest)
		return
	}

	updatedWallet, err := h.repo.ProcessBet(
		r.Context(),
		req.WalletID,
		req.Money,
		idempotencyKey,
		req.ProviderID,
		req.ExternalTransactionID,
	)

	if err != nil {
		if errors.Is(err, repository.ErrInsufficientFund) {
			http.Error(w, `{"error":"saldo insuficiente"}`, http.StatusUnprocessableEntity)
			return
		}
		if errors.Is(err, repository.ErrWalletNotFound) {
			http.Error(w, `{"error":"carteira não encontrada"}`, http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	balanceMoney, _ := domain.NewMoney(updatedWallet.Balance, updatedWallet.Currency)
	resp := BetResponse{
		TransactionID:    "tx-" + idempotencyKey,
		Status:           "PROCESSED",
		Balance:          balanceMoney,
		IdempotentReplay: false,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (h *WalletHandler) ReconcileWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	// Extrai o ID da rota: /wallets/{walletId}/reconciliation
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.Error(w, "ID da carteira inválido", http.StatusBadRequest)
		return
	}
	walletID := parts[1]

	res, err := h.repo.Reconcile(r.Context(), walletID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type ReconciliationResponse struct {
		WalletID          string       `json:"walletId"`
		StoredBalance     domain.Money `json:"storedBalance"`
		CalculatedBalance domain.Money `json:"calculatedBalance"`
		Difference        domain.Money `json:"difference"`
		Consistent        bool         `json:"consistent"`
		CheckedEntries    int          `json:"checkedEntries"`
	}

	storedMoney, _ := domain.NewMoney(res.StoredBalance, "BRL")
	calculatedMoney, _ := domain.NewMoney(res.CalculatedBalance, "BRL")
	diffMoney, _ := domain.NewMoney(res.Difference, "BRL")

	resp := ReconciliationResponse{
		WalletID:          res.WalletID,
		StoredBalance:     storedMoney,
		CalculatedBalance: calculatedMoney,
		Difference:        diffMoney,
		Consistent:        res.Consistent,
		CheckedEntries:    res.CheckedEntries,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}