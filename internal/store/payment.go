package store

import "time"

// Payment is a settled x402 payment record.
type Payment struct {
	ID               int64     `json:"id"`
	FromAddress      string    `json:"from_address"`
	ToAddress        string    `json:"to_address"`
	Amount           int64     `json:"amount"` // USDC base units (6 decimals)
	TxHash           string    `json:"tx_hash"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	RequestID        string    `json:"request_id"`
	Network          string    `json:"network"`
	CreatedAt        time.Time `json:"created_at"`
}

// Record persists a settled payment.
func (d *DB) Record(p *Payment) error {
	_, err := d.sql.Exec(
		`INSERT INTO payments
		 (from_address, to_address, amount, tx_hash, model, prompt_tokens, completion_tokens, request_id, network)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		p.FromAddress, p.ToAddress, p.Amount, p.TxHash, p.Model,
		p.PromptTokens, p.CompletionTokens, p.RequestID, p.Network,
	)
	return err
}

// SumByAddress returns the total spent and payment count for an address.
func (d *DB) SumByAddress(addr string) (total int64, count int64, err error) {
	row := d.sql.QueryRow(
		`SELECT COALESCE(SUM(amount),0), COUNT(*) FROM payments WHERE from_address = ?`, addr)
	err = row.Scan(&total, &count)
	return
}

// RecentByAddress returns the most recent payments for an address (up to 50).
func (d *DB) RecentByAddress(addr string, limit int) ([]Payment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.sql.Query(
		`SELECT id, from_address, to_address, amount, tx_hash, model,
		        prompt_tokens, completion_tokens, request_id, network, created_at
		 FROM payments WHERE from_address = ?
		 ORDER BY created_at DESC LIMIT ?`, addr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payment
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.FromAddress, &p.ToAddress, &p.Amount, &p.TxHash,
			&p.Model, &p.PromptTokens, &p.CompletionTokens, &p.RequestID, &p.Network, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) RecentPayments(limit int) ([]Payment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.sql.Query(
		`SELECT id, from_address, to_address, amount, tx_hash, model,
		        prompt_tokens, completion_tokens, request_id, network, created_at
		 FROM payments
		 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payment
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.FromAddress, &p.ToAddress, &p.Amount, &p.TxHash,
			&p.Model, &p.PromptTokens, &p.CompletionTokens, &p.RequestID, &p.Network, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type Summary struct {
	TotalAmount      int64 `json:"totalAmount"`
	PaymentCount     int64 `json:"paymentCount"`
	UniquePayers     int64 `json:"uniquePayers"`
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
}

func (d *DB) Summary() (Summary, error) {
	var s Summary
	row := d.sql.QueryRow(`SELECT
		COALESCE(SUM(amount),0),
		COUNT(*),
		COUNT(DISTINCT from_address),
		COALESCE(SUM(prompt_tokens),0),
		COALESCE(SUM(completion_tokens),0)
	FROM payments`)
	err := row.Scan(&s.TotalAmount, &s.PaymentCount, &s.UniquePayers, &s.PromptTokens, &s.CompletionTokens)
	return s, err
}
