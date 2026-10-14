package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"backend-challenge-go/internal/handler"
	"backend-challenge-go/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// Provedor de conexão com o PostgreSQL
func NewDatabasePool(lc fx.Lifecycle) (*pgxpool.Pool, error) {
	connStr := "postgres://postgres:password@localhost:5432/betting_db?sslmode=disable"
	if env := os.Getenv("DATABASE_URL"); env != "" {
		connStr = env
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar no postgres: %w", err)
	}

	// Registra o fechamento da pool no ciclo de vida do Uber Fx
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			pool.Close()
			fmt.Println("Conexão com PostgreSQL fechada com sucesso.")
			return nil
		},
	})

	return pool, nil
}

// Configuração do servidor HTTP e roteamento
func NewHTTPServer(lc fx.Lifecycle, h *handler.WalletHandler) *http.Server {
	mux := http.NewServeMux()

	// Registra as rotas da API
	mux.HandleFunc("/wallets", h.CreateWallet)
	mux.HandleFunc("/wagering/transactions", h.ProcessTransaction)
	mux.HandleFunc("/wallets/", func(w http.ResponseWriter, r *http.Request) {
		if stringsHasSuffix(r.URL.Path, "/reconciliation") {
			h.ReconcileWallet(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// Health check exigido no desafio
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"UP"}`))
	})

	srv := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	// Uber Fx gerencia o Start e o Stop gracioso do servidor
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			ln, err := net.Listen("tcp", srv.Addr)
			if err != nil {
				return err
			}
			fmt.Println("Servidor HTTP rodando na porta 8080...")
			go srv.Serve(ln)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			fmt.Println("Encerrando servidor HTTP...")
			return srv.Shutdown(ctx)
		},
	})

	return srv
}

func stringsHasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func main() {
	app := fx.New(
		fx.Provide(
			NewDatabasePool,
			repository.NewWalletRepository,
			handler.NewWalletHandler,
			NewHTTPServer,
		),
		fx.Invoke(func(*http.Server) {}), // Garante a inicialização do servidor
	)

	app.Run()
}
