package db

import (
	"database/sql"
	"fmt"

	"rc_stewarthuang/internal/model"
)

func (s *Store) CreateSupplier(sup *model.Supplier) error {
	res, err := s.DB.Exec(
		`INSERT INTO suppliers (name, url, method, headers, retry_max_attempts, retry_base_delay_ms, retry_max_delay_ms, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sup.Name, sup.URL, sup.Method, sup.Headers,
		sup.RetryMaxAttempts, sup.RetryBaseDelayMs, sup.RetryMaxDelayMs,
		sup.Enabled, sup.CreatedAt, sup.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create supplier: %w", err)
	}
	id, _ := res.LastInsertId()
	sup.ID = id
	return nil
}

func (s *Store) GetSupplier(name string) (*model.Supplier, error) {
	row := s.DB.QueryRow(
		`SELECT id, name, url, method, headers, retry_max_attempts, retry_base_delay_ms, retry_max_delay_ms, enabled, created_at, updated_at
		 FROM suppliers WHERE name = ?`, name)
	sup := &model.Supplier{}
	err := row.Scan(&sup.ID, &sup.Name, &sup.URL, &sup.Method, &sup.Headers,
		&sup.RetryMaxAttempts, &sup.RetryBaseDelayMs, &sup.RetryMaxDelayMs,
		&sup.Enabled, &sup.CreatedAt, &sup.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("supplier %q not found", name)
		}
		return nil, fmt.Errorf("get supplier: %w", err)
	}
	return sup, nil
}

func (s *Store) ListSuppliers() ([]model.Supplier, error) {
	rows, err := s.DB.Query(
		`SELECT id, name, url, method, headers, retry_max_attempts, retry_base_delay_ms, retry_max_delay_ms, enabled, created_at, updated_at
		 FROM suppliers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list suppliers: %w", err)
	}
	defer rows.Close()
	var result []model.Supplier
	for rows.Next() {
		var sup model.Supplier
		if err := rows.Scan(&sup.ID, &sup.Name, &sup.URL, &sup.Method, &sup.Headers,
			&sup.RetryMaxAttempts, &sup.RetryBaseDelayMs, &sup.RetryMaxDelayMs,
			&sup.Enabled, &sup.CreatedAt, &sup.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan supplier: %w", err)
		}
		result = append(result, sup)
	}
	return result, rows.Err()
}

func (s *Store) UpdateSupplier(sup *model.Supplier) error {
	res, err := s.DB.Exec(
		`UPDATE suppliers SET url=?, method=?, headers=?, retry_max_attempts=?, retry_base_delay_ms=?, retry_max_delay_ms=?, enabled=?, updated_at=?
		 WHERE name=?`,
		sup.URL, sup.Method, sup.Headers,
		sup.RetryMaxAttempts, sup.RetryBaseDelayMs, sup.RetryMaxDelayMs,
		sup.Enabled, sup.UpdatedAt, sup.Name)
	if err != nil {
		return fmt.Errorf("update supplier: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("supplier %q not found", sup.Name)
	}
	return nil
}

func (s *Store) DeleteSupplier(name string) error {
	res, err := s.DB.Exec(`DELETE FROM suppliers WHERE name=?`, name)
	if err != nil {
		return fmt.Errorf("delete supplier: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("supplier %q not found", name)
	}
	return nil
}

func (s *Store) SyncSuppliersFromConfig(entries []model.Supplier) error {
	for _, e := range entries {
		existing, err := s.GetSupplier(e.Name)
		if err != nil {
			if err := s.CreateSupplier(&e); err != nil {
				return fmt.Errorf("sync supplier %q: %w", e.Name, err)
			}
			continue
		}
		e.ID = existing.ID
		if err := s.UpdateSupplier(&e); err != nil {
			return fmt.Errorf("sync update supplier %q: %w", e.Name, err)
		}
	}
	return nil
}
