package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
	_ "modernc.org/sqlite"
)

type SQLite struct{ db *sql.DB }

func Open(filename string) (*SQLite, error) {
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &SQLite{db: db}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS videos(id TEXT PRIMARY KEY,title TEXT NOT NULL,status TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,size INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS files(key TEXT PRIMARY KEY,size INTEGER NOT NULL,sha256 TEXT NOT NULL,owner TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *SQLite) Close() error                   { return s.db.Close() }
func (s *SQLite) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *SQLite) Create(ctx context.Context, v Video) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO videos(id,title,status,error,created_at,size) VALUES(?,?,?,?,?,?)`, v.ID, v.Title, v.Status, v.Error, v.CreatedAt.UTC().Format(time.RFC3339Nano), v.Size)
	return err
}
func scanVideo(row interface{ Scan(...any) error }) (Video, error) {
	var v Video
	var created string
	err := row.Scan(&v.ID, &v.Title, &v.Status, &v.Error, &created, &v.Size)
	if err != nil {
		return v, metadataError(err)
	}
	v.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return v, err
}
func (s *SQLite) Get(ctx context.Context, id string) (Video, error) {
	return scanVideo(s.db.QueryRowContext(ctx, `SELECT id,title,status,error,created_at,size FROM videos WHERE id=?`, id))
}
func (s *SQLite) List(ctx context.Context) ([]Video, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,status,error,created_at,size FROM videos ORDER BY created_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	videos := make([]Video, 0)
	for rows.Next() {
		v, e := scanVideo(rows)
		if e != nil {
			return nil, e
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}
func (s *SQLite) SetStatus(ctx context.Context, id, state, message string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE videos SET status=?,error=? WHERE id=?`, state, message, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrNotFound
	}
	return err
}
func (s *SQLite) PutFile(ctx context.Context, f cluster.File) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO files(key,size,sha256,owner) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET size=excluded.size,sha256=excluded.sha256,owner=excluded.owner`, f.Key, f.Size, f.SHA256, f.Owner)
	return err
}
func (s *SQLite) Files(ctx context.Context) ([]cluster.File, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,size,sha256,owner FROM files ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]cluster.File, 0)
	for rows.Next() {
		var f cluster.File
		if err = rows.Scan(&f.Key, &f.Size, &f.SHA256, &f.Owner); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
func (s *SQLite) Nodes(ctx context.Context) ([]cluster.Node, error) {
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='nodes'`).Scan(&value); err != nil {
		return nil, metadataError(err)
	}
	var nodes []cluster.Node
	err := json.Unmarshal([]byte(value), &nodes)
	return nodes, err
}
func (s *SQLite) InitializeNodes(ctx context.Context, initial []cluster.Node, vnodes int) ([]cluster.Node, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	value, err := json.Marshal(initial)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key,value) VALUES('nodes',?)`, string(value)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key,value) VALUES('virtual_nodes',?)`, fmt.Sprint(vnodes)); err != nil {
		return nil, err
	}
	var stored string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='virtual_nodes'`).Scan(&stored); err != nil {
		return nil, err
	}
	if stored != fmt.Sprint(vnodes) {
		return nil, fmt.Errorf("virtual node count differs from persisted ring: %s", stored)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.Nodes(ctx)
}

// CommitTopology atomically commits membership and all file ownership.
func (s *SQLite) CommitTopology(ctx context.Context, nodes []cluster.Node, files []cluster.File, migration cluster.Migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	value, err := json.Marshal(nodes)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settings SET value=? WHERE key='nodes'`, string(value)); err != nil {
		return err
	}
	for _, f := range files {
		result, e := tx.ExecContext(ctx, `UPDATE files SET owner=? WHERE key=? AND size=? AND sha256=?`, f.Owner, f.Key, f.Size, f.SHA256)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("inventory changed for %s", f.Key)
		}
	}
	migrationJSON, err := json.Marshal(migration)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('migration',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(migrationJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) File(ctx context.Context, key string) (cluster.File, error) {
	var f cluster.File
	err := s.db.QueryRowContext(ctx, `SELECT key,size,sha256,owner FROM files WHERE key=?`, key).Scan(&f.Key, &f.Size, &f.SHA256, &f.Owner)
	return f, metadataError(err)
}

func (s *SQLite) SaveMigration(ctx context.Context, m cluster.Migration) error {
	value, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('migration',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(value))
	return err
}
func (s *SQLite) Migration(ctx context.Context) (cluster.Migration, error) {
	var value string
	var m cluster.Migration
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='migration'`).Scan(&value)
	if err == sql.ErrNoRows {
		return cluster.Migration{State: "idle"}, nil
	}
	if err != nil {
		return m, err
	}
	err = json.Unmarshal([]byte(value), &m)
	return m, err
}

func (s *SQLite) RecoverInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE videos SET status='failed',error='server restarted during upload or processing; upload again' WHERE status IN ('uploading','processing')`)
	return err
}

func metadataError(err error) error {
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	return err
}
