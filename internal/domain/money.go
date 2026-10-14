package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrInvalidMoneyFormat = errors.New("formato monetário inválido")
	ErrNegativeMoney      = errors.New("valor monetário não pode ser negativo na entrada")
	ErrCurrencyMismatch   = errors.New("moedas incompatíveis")
	ErrOverflow           = errors.New("estouro numérico no cálculo de money")

	moneyRegex = regexp.MustCompile(`^[0-9]+\.[0-9]{2}$`)
)

// Money armazena o valor em centavos como int64 para evitar qualquer float.
type Money struct {
	amount   int64
	currency string
}

func NewMoney(amount int64, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return Money{}, errors.New("moeda deve ter 3 caracteres ISO 4217")
	}
	return Money{amount: amount, currency: currency}, nil
}

// ParseMoney converte "25.00" para 2500 centavos sem usar float
func ParseMoney(amountStr, currency string) (Money, error) {
	amountStr = strings.TrimSpace(amountStr)
	if !moneyRegex.MatchString(amountStr) {
		return Money{}, ErrInvalidMoneyFormat
	}

	parts := strings.Split(amountStr, ".")
	integers, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Money{}, ErrInvalidMoneyFormat
	}

	cents, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Money{}, ErrInvalidMoneyFormat
	}

	// Proteção contra overflow em int64
	const maxInt = int64(1<<63 - 1)
	if integers > (maxInt-cents)/100 {
		return Money{}, ErrOverflow
	}

	totalCents := integers*100 + cents
	if totalCents < 0 {
		return Money{}, ErrNegativeMoney
	}

	return NewMoney(totalCents, currency)
}

func (m Money) Amount() int64 {
	return m.amount
}

func (m Money) Currency() string {
	return m.currency
}

func (m Money) StringAmount() string {
	cents := m.amount % 100
	integers := m.amount / 100
	return fmt.Sprintf("%d.%02d", integers, cents)
}

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	res := m.amount + other.amount
	if (res < m.amount) != (other.amount < 0) {
		return Money{}, ErrOverflow
	}
	return Money{amount: res, currency: m.currency}, nil
}

func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	res := m.amount - other.amount
	if (res > m.amount) != (other.amount < 0) {
		return Money{}, ErrOverflow
	}
	return Money{amount: res, currency: m.currency}, nil
}

// Serialização customizada para JSON: {"amount": "25.00", "currency": "BRL"}
func (m Money) MarshalJSON() ([]byte, error) {
	type moneyDTO struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}
	return json.Marshal(moneyDTO{
		Amount:   m.StringAmount(),
		Currency: m.currency,
	})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	type moneyDTO struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}
	var dto moneyDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	parsed, err := ParseMoney(dto.Amount, dto.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}