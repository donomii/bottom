package main

import (
	"database/sql"
	"strings"
	"time"
)

const recordingQueryParameters = `WITH supplied_parameters AS (
	SELECT @kind, @exit_code, @user, @cwd, @exe, @container, @unit, @parent_pid,
		@min_duration_ms, @max_duration_ms, @since, @until, @sql_limit
)
`

type recordingQuerySource struct {
	gap    bool
	legacy bool
	rank   int
}

var recordingQuerySources = []recordingQuerySource{
	{rank: 0},
	{gap: true, rank: 1},
	{legacy: true, rank: 2},
	{gap: true, legacy: true, rank: 3},
}

type recordingSQLFilter struct {
	kind              string
	exitCode          int
	user              string
	cwd               string
	exe               string
	container         string
	unit              string
	parentPID         int
	minDurationMillis int64
	maxDurationMillis int64
	since             string
	until             string
	limit             int64
}

func (source recordingQuerySource) accepts(filter Filter) bool {
	mode := filter.EventMode
	if source.gap {
		if mode != "" && mode != EventModeAll && mode != EventModeBoth && mode != string(EventGap) {
			return false
		}
		return !filter.HasExitCode && filter.User == "" && filter.CwdContains == "" && filter.ExeContains == "" &&
			filter.ContainerContains == "" && filter.UnitContains == "" && filter.ParentPID == 0 && filter.AncestorPID == 0
	}
	return mode != string(EventGap)
}

func (source recordingQuerySource) name() string {
	rowType := "events"
	if source.gap {
		rowType = "gaps"
	}
	if source.legacy {
		return "legacy " + rowType
	}
	return "versioned " + rowType
}

func (reader *sqliteRecordingReader) querySourceRows(source recordingQuerySource, filter Filter, limit int, explain bool) (*sql.Rows, error) {
	sqlFilter := newRecordingSQLFilter(filter, limit)
	query := buildRecordingSourceQuery(source, filter, explain)
	return reader.db.Query(
		query,
		sql.Named("kind", sqlFilter.kind),
		sql.Named("exit_code", sqlFilter.exitCode),
		sql.Named("user", sqlFilter.user),
		sql.Named("cwd", sqlFilter.cwd),
		sql.Named("exe", sqlFilter.exe),
		sql.Named("container", sqlFilter.container),
		sql.Named("unit", sqlFilter.unit),
		sql.Named("parent_pid", sqlFilter.parentPID),
		sql.Named("min_duration_ms", sqlFilter.minDurationMillis),
		sql.Named("max_duration_ms", sqlFilter.maxDurationMillis),
		sql.Named("since", sqlFilter.since),
		sql.Named("until", sqlFilter.until),
		sql.Named("sql_limit", sqlFilter.limit),
	)
}

func buildRecordingSourceQuery(source recordingQuerySource, filter Filter, explain bool) string {
	var query strings.Builder
	if explain {
		query.WriteString("EXPLAIN QUERY PLAN ")
	}
	query.WriteString(recordingQueryParameters)
	if source.gap {
		writeRecordingGapSelect(&query, source, filter)
	} else {
		writeRecordingEventSelect(&query, source, filter)
	}
	table := "events"
	if source.gap {
		table = "gaps"
	}
	query.WriteString(" ORDER BY " + table + ".time_key, " + table + ".sequence, " + table + ".id")
	query.WriteString(" LIMIT @sql_limit")
	return query.String()
}

func writeRecordingEventSelect(query *strings.Builder, source recordingQuerySource, filter Filter) {
	query.WriteString(`SELECT
	0 AS source, COALESCE(event_json, '') AS encoded_event,
	COALESCE(session_id, recording_session_id, '') AS session_id,
	COALESCE(schema_version, 0) AS schema_version, COALESCE(sequence, 0) AS sequence,
	time AS event_time, time_key AS event_time_key, COALESCE(observed_at, '') AS observed_at, kind AS event_kind,
	COALESCE(host, '') AS host, COALESCE(boot_id, '') AS boot_id, COALESCE(process_id, '') AS process_id,
	COALESCE(pid, 0) AS pid, COALESCE(parent_pid, 0) AS parent_pid, COALESCE(user, '') AS user_name,
	COALESCE(uid, '') AS uid, COALESCE(command, '') AS command, COALESCE(exe, '') AS exe,
	COALESCE(cwd, '') AS cwd, COALESCE(tty, '') AS tty, COALESCE(process_session, '') AS process_session,
	COALESCE(cgroup, '') AS cgroup, COALESCE(systemd_unit, '') AS systemd_unit,
	COALESCE(container_id, '') AS container_id, COALESCE(duration_ms, 0) AS duration_ms,
	exit_code AS exit_code, COALESCE(backend, '') AS backend, COALESCE(count, 0) AS count,
	COALESCE(window_ms, 0) AS window_ms, COALESCE(message, '') AS message,
	COALESCE(parent_chain, '') AS parent_chain
	FROM events INDEXED BY `)
	query.WriteString(recordingEventIndex(source, filter))
	if source.legacy {
		query.WriteString(" WHERE (event_json IS NULL OR event_json = '')")
	} else {
		query.WriteString(" WHERE event_json IS NOT NULL AND event_json != ''")
	}
	if filter.EventMode != "" && filter.EventMode != EventModeAll && filter.EventMode != EventModeBoth {
		query.WriteString(" AND kind = @kind")
	}
	if filter.HasExitCode {
		query.WriteString(" AND exit_code = @exit_code")
	}
	if filter.User != "" {
		query.WriteString(" AND (user = @user OR uid = @user)")
	}
	if filter.CwdContains != "" {
		query.WriteString(" AND instr(COALESCE(cwd, ''), @cwd) > 0")
	}
	if filter.ExeContains != "" {
		query.WriteString(" AND instr(COALESCE(exe, ''), @exe) > 0")
	}
	if filter.ContainerContains != "" {
		query.WriteString(" AND instr(COALESCE(container_id, ''), @container) > 0")
	}
	if filter.UnitContains != "" {
		query.WriteString(" AND instr(COALESCE(systemd_unit, ''), @unit) > 0")
	}
	if filter.ParentPID != 0 {
		query.WriteString(" AND parent_pid = @parent_pid")
	}
	if filter.MinDuration > 0 {
		query.WriteString(" AND (kind != 'stop' OR duration_ms >= @min_duration_ms)")
	}
	if filter.MaxDuration > 0 {
		query.WriteString(" AND (kind != 'stop' OR duration_ms <= @max_duration_ms)")
	}
	writeRecordingTimeConditions(query, filter)
}

func writeRecordingGapSelect(query *strings.Builder, source recordingQuerySource, filter Filter) {
	query.WriteString(`SELECT
	1 AS source, COALESCE(event_json, '') AS encoded_event,
	COALESCE(session_id, recording_session_id, '') AS session_id,
	COALESCE(schema_version, 0) AS schema_version, COALESCE(sequence, 0) AS sequence,
	time AS event_time, time_key AS event_time_key, COALESCE(observed_at, '') AS observed_at, 'gap' AS event_kind,
	COALESCE(host, '') AS host, COALESCE(boot_id, '') AS boot_id, '' AS process_id,
	0 AS pid, 0 AS parent_pid, '' AS user_name, '' AS uid, '' AS command, '' AS exe, '' AS cwd,
	'' AS tty, '' AS process_session, '' AS cgroup, '' AS systemd_unit, '' AS container_id,
	0 AS duration_ms, NULL AS exit_code, COALESCE(backend, '') AS backend,
	COALESCE(count, 0) AS count, 0 AS window_ms, COALESCE(message, '') AS message, '' AS parent_chain
	FROM gaps INDEXED BY `)
	if source.legacy {
		query.WriteString("gaps_legacy_time WHERE (event_json IS NULL OR event_json = '')")
	} else {
		query.WriteString("gaps_time WHERE event_json IS NOT NULL AND event_json != ''")
	}
	writeRecordingTimeConditions(query, filter)
}

func writeRecordingTimeConditions(query *strings.Builder, filter Filter) {
	if !filter.Since.IsZero() {
		query.WriteString(" AND time_key >= @since")
	}
	if !filter.Until.IsZero() {
		query.WriteString(" AND time_key <= @until")
	}
}

func recordingEventIndex(source recordingQuerySource, filter Filter) string {
	if source.legacy {
		return "events_legacy_time"
	}
	if filter.EventMode != "" && filter.EventMode != EventModeAll && filter.EventMode != EventModeBoth {
		return "events_kind_time"
	}
	if filter.ParentPID != 0 {
		return "events_parent_time"
	}
	if filter.HasExitCode {
		return "events_exit_time"
	}
	return "events_time"
}

func newRecordingSQLFilter(filter Filter, limit int) recordingSQLFilter {
	value := recordingSQLFilter{
		kind:              filter.EventMode,
		exitCode:          filter.ExitCode,
		user:              filter.User,
		cwd:               filter.CwdContains,
		exe:               filter.ExeContains,
		container:         filter.ContainerContains,
		unit:              filter.UnitContains,
		parentPID:         filter.ParentPID,
		minDurationMillis: durationCeilingMillis(filter.MinDuration),
		maxDurationMillis: int64(filter.MaxDuration / time.Millisecond),
		limit:             -1,
	}
	if !filter.Since.IsZero() {
		value.since = formatRecordingTimeKey(filter.Since)
	}
	if !filter.Until.IsZero() {
		value.until = formatRecordingTimeKey(filter.Until)
	}
	if limit > 0 && !recordingFilterNeedsPostScan(filter) {
		value.limit = int64(limit)
	}
	return value
}

func durationCeilingMillis(value time.Duration) int64 {
	milliseconds := value / time.Millisecond
	if value%time.Millisecond != 0 {
		milliseconds++
	}
	return int64(milliseconds)
}

func recordingFilterNeedsPostScan(filter Filter) bool {
	return filter.AncestorPID != 0 ||
		len(filter.Include) > 0 || len(filter.Exclude) > 0 || len(filter.IncludeRegex) > 0 || len(filter.ExcludeRegex) > 0
}
