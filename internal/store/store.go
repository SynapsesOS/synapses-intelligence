// Package store manages the brain's own SQLite database.
// It stores semantic summaries, cached violation explanations, SDLC state,
// decision history, and learned co-occurrence patterns.
// This is separate from Synapses' SQLite database.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS semantic_summaries (
	node_id    TEXT PRIMARY KEY,
	node_name  TEXT NOT NULL,
	summary    TEXT NOT NULL,
	tags       TEXT NOT NULL DEFAULT '[]',
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS violation_cache (
	rule_id      TEXT NOT NULL,
	source_file  TEXT NOT NULL,
	explanation  TEXT NOT NULL,
	fix          TEXT NOT NULL,
	cached_at    TEXT NOT NULL,
	PRIMARY KEY (rule_id, source_file)
);

CREATE TABLE IF NOT EXISTS sdlc_config (
	id           TEXT PRIMARY KEY DEFAULT 'default',
	phase        TEXT NOT NULL DEFAULT 'development',
	quality_mode TEXT NOT NULL DEFAULT 'standard',
	updated_at   TEXT NOT NULL,
	updated_by   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS context_patterns (
	id          TEXT PRIMARY KEY,
	trigger     TEXT NOT NULL,
	co_change   TEXT NOT NULL,
	reason      TEXT NOT NULL DEFAULT '',
	co_count    INTEGER NOT NULL DEFAULT 1,
	total_count INTEGER NOT NULL DEFAULT 1,
	confidence  REAL NOT NULL DEFAULT 1.0,
	updated_at  TEXT NOT NULL,
	UNIQUE(trigger, co_change)
);
CREATE INDEX IF NOT EXISTS idx_patterns_trigger    ON context_patterns(trigger);
CREATE INDEX IF NOT EXISTS idx_patterns_confidence ON context_patterns(confidence DESC);

CREATE TABLE IF NOT EXISTS decision_log (
	id               TEXT PRIMARY KEY,
	agent_id         TEXT NOT NULL DEFAULT '',
	phase            TEXT NOT NULL DEFAULT '',
	entity_name      TEXT NOT NULL,
	action           TEXT NOT NULL,
	related_entities TEXT NOT NULL DEFAULT '[]',
	outcome          TEXT NOT NULL DEFAULT '',
	notes            TEXT NOT NULL DEFAULT '',
	created_at       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dlog_entity  ON decision_log(entity_name);
CREATE INDEX IF NOT EXISTS idx_dlog_created ON decision_log(created_at);
`

// Store is the brain's persistent SQLite store.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the brain SQLite database at the given path.
// Parent directories are created if they do not exist.
// Old data is pruned at startup (decision_log >30d, stale patterns >14d).
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Store{db: db}
	s.pruneOldData()
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// pruneOldData removes stale entries at startup to keep brain.sqlite small.
func (s *Store) pruneOldData() {
	now := time.Now().UTC()
	// Prune decision log entries older than 30 days.
	cutoff30d := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	s.db.Exec(`DELETE FROM decision_log WHERE created_at < ?`, cutoff30d)
	// Prune weak, stale patterns (seen < 2 times AND older than 14 days).
	cutoff14d := now.Add(-14 * 24 * time.Hour).Format(time.RFC3339)
	s.db.Exec(`DELETE FROM context_patterns WHERE co_count < 2 AND updated_at < ?`, cutoff14d)
}

// --- Semantic Summaries ---

// UpsertSummary stores or updates the semantic summary and tags for a node.
func (s *Store) UpsertSummary(nodeID, nodeName, summary string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO semantic_summaries (node_id, node_name, summary, tags, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			node_name  = excluded.node_name,
			summary    = excluded.summary,
			tags       = excluded.tags,
			updated_at = excluded.updated_at`,
		nodeID, nodeName, summary, string(tagsJSON), now,
	)
	return err
}

// GetSummary returns the stored summary for a node, or "" if not found.
func (s *Store) GetSummary(nodeID string) string {
	var summary string
	err := s.db.QueryRow(
		`SELECT summary FROM semantic_summaries WHERE node_id = ?`, nodeID,
	).Scan(&summary)
	if err != nil {
		return ""
	}
	return summary
}

// GetSummaryWithTags returns summary and tags for a node.
func (s *Store) GetSummaryWithTags(nodeID string) (summary string, tags []string) {
	var tagsJSON string
	err := s.db.QueryRow(
		`SELECT summary, tags FROM semantic_summaries WHERE node_id = ?`, nodeID,
	).Scan(&summary, &tagsJSON)
	if err != nil {
		return "", nil
	}
	json.Unmarshal([]byte(tagsJSON), &tags)
	return summary, tags
}

// GetSummaries returns summaries for all given node IDs keyed by node ID.
// Missing nodes are omitted from the result map.
func (s *Store) GetSummaries(nodeIDs []string) map[string]string {
	result := make(map[string]string, len(nodeIDs))
	for _, id := range nodeIDs {
		if sm := s.GetSummary(id); sm != "" {
			result[id] = sm
		}
	}
	return result
}

// GetSummariesByName returns summaries keyed by node_name for the given names.
// This is used by the contextbuilder to look up dep summaries by entity name.
func (s *Store) GetSummariesByName(names []string) map[string]string {
	if len(names) == 0 {
		return map[string]string{}
	}
	placeholders := make([]string, len(names))
	args := make([]interface{}, len(names))
	for i, n := range names {
		placeholders[i] = "?"
		args[i] = n
	}
	query := fmt.Sprintf(
		`SELECT node_name, summary FROM semantic_summaries WHERE node_name IN (%s)`,
		strings.Join(placeholders, ","),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return map[string]string{}
	}
	defer rows.Close()
	result := make(map[string]string, len(names))
	for rows.Next() {
		var name, summary string
		if rows.Scan(&name, &summary) == nil {
			result[name] = summary
		}
	}
	return result
}

// SummaryCount returns the total number of stored summaries.
func (s *Store) SummaryCount() int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM semantic_summaries`).Scan(&n)
	return n
}

// AllSummaries returns all stored summaries as a slice for the CLI display.
func (s *Store) AllSummaries() ([]Summary, error) {
	rows, err := s.db.Query(
		`SELECT node_id, node_name, summary, updated_at FROM semantic_summaries ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var sm Summary
		if err := rows.Scan(&sm.NodeID, &sm.NodeName, &sm.Summary, &sm.UpdatedAt); err != nil {
			continue
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// Summary is a row from semantic_summaries.
type Summary struct {
	NodeID    string
	NodeName  string
	Summary   string
	UpdatedAt string
}

// --- Violation Cache ---

// UpsertViolationExplanation caches a plain-English explanation for a rule+file pair.
func (s *Store) UpsertViolationExplanation(ruleID, sourceFile, explanation, fix string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO violation_cache (rule_id, source_file, explanation, fix, cached_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(rule_id, source_file) DO UPDATE SET
			explanation = excluded.explanation,
			fix         = excluded.fix,
			cached_at   = excluded.cached_at`,
		ruleID, sourceFile, explanation, fix, now,
	)
	return err
}

// GetViolationExplanation returns the cached explanation for a rule+file pair.
// Returns ("", "", false) if not cached.
func (s *Store) GetViolationExplanation(ruleID, sourceFile string) (explanation, fix string, found bool) {
	err := s.db.QueryRow(
		`SELECT explanation, fix FROM violation_cache WHERE rule_id = ? AND source_file = ?`,
		ruleID, sourceFile,
	).Scan(&explanation, &fix)
	if err != nil {
		return "", "", false
	}
	return explanation, fix, true
}

// --- SDLC Config ---

// SDLCConfigRow is the stored project SDLC state.
type SDLCConfigRow struct {
	Phase       string
	QualityMode string
	UpdatedAt   string
	UpdatedBy   string
}

// GetSDLCConfig returns the current project SDLC config.
// Returns defaults if no config row exists.
func (s *Store) GetSDLCConfig() SDLCConfigRow {
	var row SDLCConfigRow
	err := s.db.QueryRow(
		`SELECT phase, quality_mode, updated_at, updated_by FROM sdlc_config WHERE id = 'default'`,
	).Scan(&row.Phase, &row.QualityMode, &row.UpdatedAt, &row.UpdatedBy)
	if err != nil {
		return SDLCConfigRow{
			Phase:       "development",
			QualityMode: "standard",
			UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		}
	}
	return row
}

// UpsertSDLCConfig saves the SDLC config row.
func (s *Store) UpsertSDLCConfig(phase, qualityMode, updatedBy string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO sdlc_config (id, phase, quality_mode, updated_at, updated_by)
		VALUES ('default', ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			phase        = excluded.phase,
			quality_mode = excluded.quality_mode,
			updated_at   = excluded.updated_at,
			updated_by   = excluded.updated_by`,
		phase, qualityMode, now, updatedBy,
	)
	return err
}

// --- Context Patterns (co-occurrence learning) ---

// ContextPattern is a learned co-occurrence rule.
type ContextPattern struct {
	Trigger    string
	CoChange   string
	Reason     string
	Confidence float64
	CoCount    int
}

// GetPatternsForTriggers returns the top patterns for any of the given trigger names.
// Results are ordered by confidence descending, capped at limit.
func (s *Store) GetPatternsForTriggers(triggers []string, limit int) []ContextPattern {
	if len(triggers) == 0 || limit <= 0 {
		return nil
	}
	placeholders := make([]string, len(triggers))
	args := make([]interface{}, len(triggers))
	for i, t := range triggers {
		placeholders[i] = "?"
		args[i] = t
	}
	args = append(args, limit)
	query := fmt.Sprintf(
		`SELECT trigger, co_change, reason, confidence, co_count
		 FROM context_patterns
		 WHERE trigger IN (%s)
		 ORDER BY confidence DESC
		 LIMIT ?`,
		strings.Join(placeholders, ","),
	)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ContextPattern
	for rows.Next() {
		var p ContextPattern
		if rows.Scan(&p.Trigger, &p.CoChange, &p.Reason, &p.Confidence, &p.CoCount) == nil {
			out = append(out, p)
		}
	}
	return out
}

// AllPatterns returns all patterns ordered by confidence for CLI display.
func (s *Store) AllPatterns() ([]ContextPattern, error) {
	rows, err := s.db.Query(
		`SELECT trigger, co_change, reason, confidence, co_count
		 FROM context_patterns ORDER BY confidence DESC, co_count DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContextPattern
	for rows.Next() {
		var p ContextPattern
		if rows.Scan(&p.Trigger, &p.CoChange, &p.Reason, &p.Confidence, &p.CoCount) == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// UpsertPattern adds or increments a co-occurrence pattern.
// coCount and totalCount are incremented; confidence is recomputed.
func (s *Store) UpsertPattern(trigger, coChange, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	// Truncate reason to 100 chars.
	if len(reason) > 100 {
		reason = reason[:100]
	}
	id := fmt.Sprintf("%s::%s", trigger, coChange)
	_, err := s.db.Exec(`
		INSERT INTO context_patterns (id, trigger, co_change, reason, co_count, total_count, confidence, updated_at)
		VALUES (?, ?, ?, ?, 1, 1, 1.0, ?)
		ON CONFLICT(trigger, co_change) DO UPDATE SET
			co_count    = co_count + 1,
			total_count = total_count + 1,
			confidence  = CAST(co_count + 1 AS REAL) / CAST(total_count + 1 AS REAL),
			reason      = CASE WHEN excluded.reason != '' THEN excluded.reason ELSE reason END,
			updated_at  = excluded.updated_at`,
		id, trigger, coChange, reason, now,
	)
	return err
}

// --- Decision Log ---

// DecisionLogEntry is a row from decision_log.
type DecisionLogEntry struct {
	ID              string
	AgentID         string
	Phase           string
	EntityName      string
	Action          string
	RelatedEntities []string
	Outcome         string
	Notes           string
	CreatedAt       string
}

// LogDecision inserts a new decision log entry.
func (s *Store) LogDecision(agentID, phase, entityName, action string, relatedEntities []string, outcome, notes string) error {
	if relatedEntities == nil {
		relatedEntities = []string{}
	}
	relJSON, _ := json.Marshal(relatedEntities)
	now := time.Now().UTC()
	id := fmt.Sprintf("%d-%s", now.UnixNano(), entityName)
	_, err := s.db.Exec(`
		INSERT INTO decision_log (id, agent_id, phase, entity_name, action, related_entities, outcome, notes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, agentID, phase, entityName, action,
		string(relJSON), outcome, notes,
		now.UTC().Format(time.RFC3339),
	)
	return err
}

// GetRecentDecisions returns the most recent decision log entries for an entity.
func (s *Store) GetRecentDecisions(entityName string, limit int) ([]DecisionLogEntry, error) {
	var rows *sql.Rows
	var err error
	if entityName != "" {
		rows, err = s.db.Query(
			`SELECT id, agent_id, phase, entity_name, action, related_entities, outcome, notes, created_at
			 FROM decision_log WHERE entity_name = ? ORDER BY created_at DESC LIMIT ?`,
			entityName, limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, agent_id, phase, entity_name, action, related_entities, outcome, notes, created_at
			 FROM decision_log ORDER BY created_at DESC LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DecisionLogEntry
	for rows.Next() {
		var e DecisionLogEntry
		var relJSON string
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Phase, &e.EntityName,
			&e.Action, &relJSON, &e.Outcome, &e.Notes, &e.CreatedAt); err != nil {
			continue
		}
		json.Unmarshal([]byte(relJSON), &e.RelatedEntities)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Reset ---

// Reset deletes all brain data.
func (s *Store) Reset() error {
	_, err := s.db.Exec(`
		DELETE FROM semantic_summaries;
		DELETE FROM violation_cache;
		DELETE FROM sdlc_config;
		DELETE FROM context_patterns;
		DELETE FROM decision_log;
	`)
	return err
}
