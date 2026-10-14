package domain_test


import (
	"backend-challenge-go/internal/domain"
	"testing"
)

func TestParseMoney(t *testing.T) {
	m, err := domain.ParseMoney("25.00", "BRL")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if m.Amount() != 2500 {
		t.Errorf("esperava 2500, recebeu %d", m.Amount())
	}
	if m.StringAmount() != "25.00" {
		t.Errorf("esperava '25.00', recebeu %s", m.StringAmount())
	}
}

func TestMoney_Subtracao(t *testing.T) {
	m1, _ := domain.ParseMoney("100.00", "BRL")
	m2, _ := domain.ParseMoney("80.00", "BRL")

	res, err := m1.Subtract(m2)
	if err != nil {
		t.Fatalf("erro ao subtrair: %v", err)
	}
	if res.StringAmount() != "20.00" {
		t.Errorf("esperava '20.00', recebeu %s", res.StringAmount())
	}
}

func TestMoney_MoedaDiferente(t *testing.T) {
	m1, _ := domain.ParseMoney("10.00", "BRL")
	m2, _ := domain.ParseMoney("10.00", "USD")

	_, err := m1.Add(m2)
	if err != domain.ErrCurrencyMismatch {
		t.Errorf("deveria falhar ao somar BRL com USD")
	}
}