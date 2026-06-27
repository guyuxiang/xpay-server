package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/payapi/x402-server/internal/pricing"
)

func (d *DB) GetSetting(key string) (string, bool, error) {
	var value string
	err := d.sql.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == nil {
		return value, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return "", false, err
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.sql.Exec(
		`INSERT INTO settings(key, value, updated_at)
		 VALUES(?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value,
	)
	return err
}

func (d *DB) BootstrapDefaultPrices(entries []pricing.ModelPriceEntry) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, entry := range entries {
		if _, err := tx.Exec(
			`INSERT INTO model_prices(model, input, output, cached_input, is_default)
			 VALUES(?, ?, ?, ?, ?)
			 ON CONFLICT(model) DO NOTHING`,
			entry.Model, entry.Input, entry.Output, entry.CachedInput, boolToInt(entry.IsDefault),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) ListModelPrices() ([]pricing.ModelPriceEntry, error) {
	rows, err := d.sql.Query(`SELECT model, input, output, cached_input, is_default FROM model_prices ORDER BY model ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pricing.ModelPriceEntry
	for rows.Next() {
		var entry pricing.ModelPriceEntry
		var isDefault int
		if err := rows.Scan(&entry.Model, &entry.Input, &entry.Output, &entry.CachedInput, &isDefault); err != nil {
			return nil, err
		}
		entry.IsDefault = isDefault != 0
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (d *DB) UpsertModelPrice(entry pricing.ModelPriceEntry) error {
	if entry.Model == "" {
		return fmt.Errorf("model is required")
	}
	_, err := d.sql.Exec(
		`INSERT INTO model_prices(model, input, output, cached_input, is_default, updated_at)
		 VALUES(?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(model) DO UPDATE SET
		   input = excluded.input,
		   output = excluded.output,
		   cached_input = excluded.cached_input,
		   is_default = excluded.is_default,
		   updated_at = CURRENT_TIMESTAMP`,
		entry.Model, entry.Input, entry.Output, entry.CachedInput, boolToInt(entry.IsDefault),
	)
	return err
}

func (d *DB) DeleteModelPrice(model string) error {
	_, err := d.sql.Exec(`DELETE FROM model_prices WHERE model = ?`, model)
	return err
}

func (d *DB) ReplaceModelPrices(entries []pricing.ModelPriceEntry) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM model_prices`); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := tx.Exec(
			`INSERT INTO model_prices(model, input, output, cached_input, is_default) VALUES(?, ?, ?, ?, ?)`,
			entry.Model, entry.Input, entry.Output, entry.CachedInput, boolToInt(entry.IsDefault),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
