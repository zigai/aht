package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
)

type indexConnection struct {
	*sql.Conn

	waiting func()
}

type indexStatement struct {
	*sql.Stmt

	waiting func()
}

type indexRow struct {
	conn  *indexConnection
	query string
	args  []any
}

type indexTransaction struct {
	*indexConnection
}

func retryIndex[Value any](
	ctx context.Context,
	waiting func(),
	operation func() (Value, error),
) (Value, error) {
	for {
		if err := ctx.Err(); err != nil {
			var zero Value
			return zero, fmt.Errorf("wait for history index: %w", err)
		}

		value, err := operation()
		if !isIndexBusy(err) {
			return value, err
		}

		if waiting != nil {
			waiting()
		}

		runtime.Gosched()
	}
}

func (conn *indexConnection) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	return retryIndex(ctx, conn.waiting, func() (sql.Result, error) {
		return conn.Conn.ExecContext(ctx, query, args...)
	})
}

func (conn *indexConnection) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	return retryIndex(ctx, conn.waiting, func() (*sql.Rows, error) {
		return conn.Conn.QueryContext(ctx, query, args...)
	})
}

func (conn *indexConnection) PrepareContext(
	ctx context.Context,
	query string,
) (*indexStatement, error) {
	statement, err := retryIndex(ctx, conn.waiting, func() (*sql.Stmt, error) {
		return conn.Conn.PrepareContext(ctx, query)
	})
	if err != nil {
		return nil, err
	}

	return &indexStatement{Stmt: statement, waiting: conn.waiting}, nil
}

func (conn *indexConnection) queryRow(query string, args ...any) *indexRow {
	return &indexRow{conn: conn, query: query, args: args}
}

func (row *indexRow) Scan(ctx context.Context, dest ...any) error {
	_, err := retryIndex(ctx, row.conn.waiting, func() (struct{}, error) {
		return struct{}{}, row.conn.Conn.QueryRowContext(ctx, row.query, row.args...).Scan(dest...)
	})

	return err
}

func (statement *indexStatement) ExecContext(ctx context.Context, args ...any) (sql.Result, error) {
	return retryIndex(ctx, statement.waiting, func() (sql.Result, error) {
		return statement.Stmt.ExecContext(ctx, args...)
	})
}

func (statement *indexStatement) QueryContext(ctx context.Context, args ...any) (*sql.Rows, error) {
	return retryIndex(ctx, statement.waiting, func() (*sql.Rows, error) {
		return statement.Stmt.QueryContext(ctx, args...)
	})
}

func (conn *indexConnection) begin(ctx context.Context) (*indexTransaction, error) {
	// sql.Tx cannot retry Commit after SQLITE_BUSY because it marks the transaction done.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}

	return &indexTransaction{indexConnection: conn}, nil
}

func (transaction *indexTransaction) Commit(ctx context.Context) error {
	if _, err := transaction.ExecContext(ctx, "COMMIT"); err != nil {
		return errors.Join(err, transaction.Rollback(ctx))
	}

	return nil
}

func (transaction *indexTransaction) Rollback(ctx context.Context) error {
	_, err := transaction.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
	return err
}
