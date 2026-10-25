package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"ruche/internal/protocol"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS flight_sheets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	miner TEXT NOT NULL,
	algo TEXT NOT NULL,
	pool TEXT NOT NULL,
	wallet TEXT NOT NULL,
	worker TEXT NOT NULL DEFAULT '',
	extra_args TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS rigs (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	flight_sheet_id INTEGER REFERENCES flight_sheets(id) ON DELETE SET NULL,
	last_seen INTEGER NOT NULL DEFAULT 0,
	last_report TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS stats (
	rig_id TEXT NOT NULL REFERENCES rigs(id) ON DELETE CASCADE,
	ts INTEGER NOT NULL,
	hashrate REAL NOT NULL,
	max_temp REAL NOT NULL,
	power REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS stats_rig_ts ON stats(rig_id, ts);
CREATE TABLE IF NOT EXISTS commands (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	rig_id TEXT NOT NULL REFERENCES rigs(id) ON DELETE CASCADE,
	type TEXT NOT NULL,
	created INTEGER NOT NULL,
	delivered INTEGER NOT NULL DEFAULT 0
);
`

var ErrNotFound = errors.New("not found")

// Rig is a rig as shown on the dashboard.
type Rig struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	FlightSheetID *int64          `json:"flight_sheet_id"`
	LastSeen      int64           `json:"last_seen"`
	Online        bool            `json:"online"`
	Report        protocol.Report `json:"report"`
}

type Point struct {
	TS       int64   `json:"ts"`
	Hashrate float64 `json:"hashrate"`
	MaxTemp  float64 `json:"max_temp"`
	Power    float64 `json:"power"`
}

type Store struct {
	db *sql.DB
	// OfflineAfter is how long a rig may stay silent before it counts as offline.
	OfflineAfter time.Duration
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db, OfflineAfter: 2 * time.Minute}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// CreateRig returns the new rig and its secret token. Only a hash of the token is stored.
func (s *Store) CreateRig(name string) (Rig, string, error) {
	id := randomHex(6)
	token := randomHex(24)
	_, err := s.db.Exec(`INSERT INTO rigs (id, name, token_hash) VALUES (?, ?, ?)`, id, name, hashToken(token))
	return Rig{ID: id, Name: name}, token, err
}

func (s *Store) DeleteRig(id string) error {
	res, err := s.db.Exec(`DELETE FROM rigs WHERE id = ?`, id)
	return notFoundIfNone(res, err)
}

// RigByToken authenticates an agent.
func (s *Store) RigByToken(token string) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM rigs WHERE token_hash = ?`, hashToken(token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) Rigs() ([]Rig, error) {
	rows, err := s.db.Query(`SELECT id, name, flight_sheet_id, last_seen, last_report FROM rigs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rigs := []Rig{}
	for rows.Next() {
		var r Rig
		var fs sql.NullInt64
		var report string
		if err := rows.Scan(&r.ID, &r.Name, &fs, &r.LastSeen, &report); err != nil {
			return nil, err
		}
		if fs.Valid {
			r.FlightSheetID = &fs.Int64
		}
		_ = json.Unmarshal([]byte(report), &r.Report)
		r.Online = time.Since(time.Unix(r.LastSeen, 0)) < s.OfflineAfter
		rigs = append(rigs, r)
	}
	return rigs, rows.Err()
}

// SaveReport records a report and returns what the agent should do next.
func (s *Store) SaveReport(rigID string, rep protocol.Report) (protocol.ReportResponse, error) {
	var resp protocol.ReportResponse
	now := time.Now().Unix()
	raw, _ := json.Marshal(rep)
	tx, err := s.db.Begin()
	if err != nil {
		return resp, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE rigs SET last_seen = ?, last_report = ? WHERE id = ?`, now, string(raw), rigID); err != nil {
		return resp, err
	}
	var maxTemp, power float64
	for _, g := range rep.GPUs {
		maxTemp = max(maxTemp, g.TempC)
		power += g.PowerW
	}
	if rep.CPU != nil {
		maxTemp = max(maxTemp, rep.CPU.TempC)
	}
	if _, err := tx.Exec(`INSERT INTO stats (rig_id, ts, hashrate, max_temp, power) VALUES (?, ?, ?, ?, ?)`,
		rigID, now, rep.TotalHashrateHs, maxTemp, power); err != nil {
		return resp, err
	}

	rows, err := tx.Query(`SELECT id, type FROM commands WHERE rig_id = ? AND delivered = 0 ORDER BY id`, rigID)
	if err != nil {
		return resp, err
	}
	for rows.Next() {
		var c protocol.Command
		if err := rows.Scan(&c.ID, &c.Type); err != nil {
			rows.Close()
			return resp, err
		}
		resp.Commands = append(resp.Commands, c)
	}
	rows.Close()
	if _, err := tx.Exec(`UPDATE commands SET delivered = ? WHERE rig_id = ? AND delivered = 0`, now, rigID); err != nil {
		return resp, err
	}

	var fs protocol.FlightSheet
	err = tx.QueryRow(`SELECT f.id, f.name, f.miner, f.algo, f.pool, f.wallet, f.worker, f.extra_args
		FROM rigs r JOIN flight_sheets f ON f.id = r.flight_sheet_id WHERE r.id = ?`, rigID).
		Scan(&fs.ID, &fs.Name, &fs.Miner, &fs.Algo, &fs.Pool, &fs.Wallet, &fs.Worker, &fs.ExtraArgs)
	switch {
	case err == nil:
		resp.FlightSheet = &fs
	case !errors.Is(err, sql.ErrNoRows):
		return resp, err
	}
	return resp, tx.Commit()
}

func (s *Store) History(rigID string, since time.Time) ([]Point, error) {
	rows, err := s.db.Query(`SELECT ts, hashrate, max_temp, power FROM stats WHERE rig_id = ? AND ts >= ? ORDER BY ts`,
		rigID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pts := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.TS, &p.Hashrate, &p.MaxTemp, &p.Power); err != nil {
			return nil, err
		}
		pts = append(pts, p)
	}
	return pts, rows.Err()
}

// PruneStats drops samples older than the retention window.
func (s *Store) PruneStats(olderThan time.Time) error {
	_, err := s.db.Exec(`DELETE FROM stats WHERE ts < ?`, olderThan.Unix())
	return err
}

func (s *Store) QueueCommand(rigID, typ string) error {
	switch typ {
	case protocol.CmdRestartMiner, protocol.CmdStopMiner, protocol.CmdReboot:
	default:
		return errors.New("unknown command")
	}
	_, err := s.db.Exec(`INSERT INTO commands (rig_id, type, created) SELECT id, ?, ? FROM rigs WHERE id = ?`,
		typ, time.Now().Unix(), rigID)
	return err
}

func (s *Store) AssignFlightSheet(rigID string, fsID *int64) error {
	res, err := s.db.Exec(`UPDATE rigs SET flight_sheet_id = ? WHERE id = ?`, fsID, rigID)
	return notFoundIfNone(res, err)
}

func (s *Store) FlightSheets() ([]protocol.FlightSheet, error) {
	rows, err := s.db.Query(`SELECT id, name, miner, algo, pool, wallet, worker, extra_args FROM flight_sheets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.FlightSheet{}
	for rows.Next() {
		var f protocol.FlightSheet
		if err := rows.Scan(&f.ID, &f.Name, &f.Miner, &f.Algo, &f.Pool, &f.Wallet, &f.Worker, &f.ExtraArgs); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) SaveFlightSheet(f protocol.FlightSheet) (protocol.FlightSheet, error) {
	if f.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO flight_sheets (name, miner, algo, pool, wallet, worker, extra_args) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			f.Name, f.Miner, f.Algo, f.Pool, f.Wallet, f.Worker, f.ExtraArgs)
		if err != nil {
			return f, err
		}
		f.ID, err = res.LastInsertId()
		return f, err
	}
	res, err := s.db.Exec(`UPDATE flight_sheets SET name = ?, miner = ?, algo = ?, pool = ?, wallet = ?, worker = ?, extra_args = ? WHERE id = ?`,
		f.Name, f.Miner, f.Algo, f.Pool, f.Wallet, f.Worker, f.ExtraArgs, f.ID)
	return f, notFoundIfNone(res, err)
}

func (s *Store) DeleteFlightSheet(id int64) error {
	res, err := s.db.Exec(`DELETE FROM flight_sheets WHERE id = ?`, id)
	return notFoundIfNone(res, err)
}

func notFoundIfNone(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
