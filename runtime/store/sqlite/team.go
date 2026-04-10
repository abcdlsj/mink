package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/abcdlsj/mink/runtime/id"
	zsqlite "zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type Team struct {
	ID            string
	Name          string
	LeaderAgentID string
	Status        string
	TurnPolicy    string
	MaxRounds     int
	CreatedAt     string
	UpdatedAt     string
}

type TeamMember struct {
	TeamID          string
	AgentID         string
	RoleName        string
	RoleDescription string
	MemberType      string
	RuntimeAgentID  string
	ProfileHint     string
	JoinedAt        string
}

type TeamMemberProfile struct {
	RuntimeAgentID string `json:"runtime_agent_id,omitempty"`
	ProfileHint    string `json:"profile_hint,omitempty"`
}

type TeamThread struct {
	ID           string
	TeamID       string
	Title        string
	Status       string
	SessionID    string
	CurrentRound int
	CreatedAt    string
	UpdatedAt    string
}

func (db *DB) CreateTeam(ctx context.Context, name, leaderAgentID, turnPolicy string, maxRounds int) (string, error) {
	if db == nil {
		return "", nil
	}
	now := nowString()
	teamID := id.Team()
	if turnPolicy == "" {
		turnPolicy = "leader_driven"
	}
	if maxRounds <= 0 {
		maxRounds = 6
	}

	return teamID, db.Tx(ctx, func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO teams (id, workspace_id, name, leader_agent_id, status, turn_policy, max_rounds, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?)
		`, &sqlitex.ExecOptions{
			Args: []any{teamID, db.WorkspaceID(), name, leaderAgentID, turnPolicy, maxRounds, now, now},
		}); err != nil {
			return err
		}
		return sqlitex.ExecuteTransient(conn, `
			INSERT INTO team_members (team_id, agent_id, role_name, member_type, profile_json, joined_at)
			VALUES (?, ?, 'leader', 'persistent', ?, ?)
		`, &sqlitex.ExecOptions{
			Args: []any{teamID, leaderAgentID, `{"runtime_agent_id":"` + leaderAgentID + `"}`, now},
		})
	})
}

func (db *DB) GetTeam(ctx context.Context, teamID string) (Team, error) {
	var t Team
	if db == nil || teamID == "" {
		return t, nil
	}
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id, name, leader_agent_id, status, turn_policy, max_rounds, created_at, updated_at
			FROM teams WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), teamID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				t = Team{
					ID:            stmt.ColumnText(0),
					Name:          stmt.ColumnText(1),
					LeaderAgentID: stmt.ColumnText(2),
					Status:        stmt.ColumnText(3),
					TurnPolicy:    stmt.ColumnText(4),
					MaxRounds:     stmt.ColumnInt(5),
					CreatedAt:     stmt.ColumnText(6),
					UpdatedAt:     stmt.ColumnText(7),
				}
				return nil
			},
		})
	})
	return t, err
}

func (db *DB) ListTeams(ctx context.Context, status string) ([]Team, error) {
	if db == nil {
		return nil, nil
	}
	q := "SELECT id, name, leader_agent_id, status, turn_policy, max_rounds, created_at, updated_at FROM teams WHERE workspace_id = ?"
	args := []any{db.WorkspaceID()}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q += " ORDER BY updated_at DESC"

	var teams []Team
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, q, &sqlitex.ExecOptions{
			Args: args,
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				teams = append(teams, Team{
					ID:            stmt.ColumnText(0),
					Name:          stmt.ColumnText(1),
					LeaderAgentID: stmt.ColumnText(2),
					Status:        stmt.ColumnText(3),
					TurnPolicy:    stmt.ColumnText(4),
					MaxRounds:     stmt.ColumnInt(5),
					CreatedAt:     stmt.ColumnText(6),
					UpdatedAt:     stmt.ColumnText(7),
				})
				return nil
			},
		})
	})
	return teams, err
}

func (db *DB) UpdateTeamTurnPolicy(ctx context.Context, teamID, turnPolicy string) error {
	if db == nil || teamID == "" || turnPolicy == "" {
		return nil
	}
	now := nowString()
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			UPDATE teams SET turn_policy = ?, updated_at = ? WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{turnPolicy, now, db.WorkspaceID(), teamID},
		})
	})
}

func (db *DB) AddTeamMember(ctx context.Context, teamID, agentID, roleName, roleDesc, memberType string) error {
	return db.AddTeamMemberWithProfile(ctx, teamID, agentID, roleName, roleDesc, memberType, TeamMemberProfile{})
}

func (db *DB) AddTeamMemberWithProfile(ctx context.Context, teamID, agentID, roleName, roleDesc, memberType string, profile TeamMemberProfile) error {
	if db == nil {
		return nil
	}
	if memberType == "" {
		memberType = "persistent"
	}
	now := nowString()
	if profile.RuntimeAgentID == "" {
		profile.RuntimeAgentID = agentID
	}
	rawProfile, _ := json.Marshal(profile)
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		var exists bool
		if err := sqlitex.ExecuteTransient(conn, `
			SELECT 1
			FROM teams
			WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), teamID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				exists = stmt.ColumnInt(0) == 1
				return nil
			},
		}); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("team %s not found", teamID)
		}
		return sqlitex.ExecuteTransient(conn, `
			INSERT INTO team_members (team_id, agent_id, role_name, role_description, member_type, profile_json, joined_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(team_id, agent_id) DO UPDATE SET
				role_name = excluded.role_name,
				role_description = excluded.role_description,
				member_type = excluded.member_type,
				profile_json = excluded.profile_json
		`, &sqlitex.ExecOptions{
			Args: []any{teamID, agentID, roleName, roleDesc, memberType, string(rawProfile), now},
		})
	})
}

func (db *DB) RemoveTeamMember(ctx context.Context, teamID, agentID string) error {
	if db == nil {
		return nil
	}
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			DELETE FROM team_members
			WHERE team_id = ? AND agent_id = ?
			  AND EXISTS (
			    SELECT 1 FROM teams
			    WHERE teams.id = team_members.team_id AND teams.workspace_id = ?
			  )
		`, &sqlitex.ExecOptions{
			Args: []any{teamID, agentID, db.WorkspaceID()},
		})
	})
}

func (db *DB) ListTeamMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	if db == nil || teamID == "" {
		return nil, nil
	}
	var members []TeamMember
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT team_members.team_id, team_members.agent_id, team_members.role_name, team_members.role_description, team_members.member_type, team_members.profile_json, team_members.joined_at
			FROM team_members
			JOIN teams ON teams.id = team_members.team_id
			WHERE team_members.team_id = ? AND teams.workspace_id = ?
			ORDER BY joined_at ASC
		`, &sqlitex.ExecOptions{
			Args: []any{teamID, db.WorkspaceID()},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				var profile TeamMemberProfile
				rawProfile := stmt.ColumnText(5)
				if rawProfile != "" {
					_ = json.Unmarshal([]byte(rawProfile), &profile)
				}
				members = append(members, TeamMember{
					TeamID:          stmt.ColumnText(0),
					AgentID:         stmt.ColumnText(1),
					RoleName:        stmt.ColumnText(2),
					RoleDescription: stmt.ColumnText(3),
					MemberType:      stmt.ColumnText(4),
					RuntimeAgentID:  profile.RuntimeAgentID,
					ProfileHint:     profile.ProfileHint,
					JoinedAt:        stmt.ColumnText(6),
				})
				return nil
			},
		})
	})
	return members, err
}

func (db *DB) CreateThread(ctx context.Context, teamID, title, sessionID string) (string, error) {
	if db == nil {
		return "", nil
	}
	now := nowString()
	threadID := id.Thread()
	return threadID, db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		var exists bool
		if err := sqlitex.ExecuteTransient(conn, `
			SELECT 1
			FROM teams
			WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), teamID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				exists = stmt.ColumnInt(0) == 1
				return nil
			},
		}); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("team %s not found", teamID)
		}
		if err := sqlitex.ExecuteTransient(conn, `
			INSERT INTO team_threads (id, workspace_id, team_id, title, status, session_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'active', ?, ?, ?)
		`, &sqlitex.ExecOptions{
			Args: []any{threadID, db.WorkspaceID(), teamID, title, sessionID, now, now},
		}); err != nil {
			return err
		}
		return sqlitex.ExecuteTransient(conn, `
			UPDATE teams SET updated_at = ? WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{now, db.WorkspaceID(), teamID},
		})
	})
}

func (db *DB) GetThread(ctx context.Context, threadID string) (TeamThread, error) {
	var t TeamThread
	if db == nil || threadID == "" {
		return t, nil
	}
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT id, team_id, title, status, session_id, current_round, created_at, updated_at
			FROM team_threads WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), threadID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				t = TeamThread{
					ID:           stmt.ColumnText(0),
					TeamID:       stmt.ColumnText(1),
					Title:        stmt.ColumnText(2),
					Status:       stmt.ColumnText(3),
					SessionID:    stmt.ColumnText(4),
					CurrentRound: stmt.ColumnInt(5),
					CreatedAt:    stmt.ColumnText(6),
					UpdatedAt:    stmt.ColumnText(7),
				}
				return nil
			},
		})
	})
	return t, err
}

func (db *DB) ListThreads(ctx context.Context, teamID, status string) ([]TeamThread, error) {
	if db == nil || teamID == "" {
		return nil, nil
	}
	q := "SELECT id, team_id, title, status, session_id, current_round, created_at, updated_at FROM team_threads WHERE workspace_id = ? AND team_id = ?"
	args := []any{db.WorkspaceID(), teamID}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q += " ORDER BY updated_at DESC"

	var threads []TeamThread
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, q, &sqlitex.ExecOptions{
			Args: args,
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				threads = append(threads, TeamThread{
					ID:           stmt.ColumnText(0),
					TeamID:       stmt.ColumnText(1),
					Title:        stmt.ColumnText(2),
					Status:       stmt.ColumnText(3),
					SessionID:    stmt.ColumnText(4),
					CurrentRound: stmt.ColumnInt(5),
					CreatedAt:    stmt.ColumnText(6),
					UpdatedAt:    stmt.ColumnText(7),
				})
				return nil
			},
		})
	})
	return threads, err
}

func (db *DB) IncrementThreadRound(ctx context.Context, threadID string) (int, error) {
	if db == nil || threadID == "" {
		return 0, nil
	}
	now := nowString()
	var round int
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		if err := sqlitex.ExecuteTransient(conn, `
			UPDATE team_threads SET current_round = current_round + 1, updated_at = ? WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{now, db.WorkspaceID(), threadID},
		}); err != nil {
			return err
		}
		return sqlitex.ExecuteTransient(conn, `
			SELECT current_round FROM team_threads WHERE workspace_id = ? AND id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), threadID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				round = stmt.ColumnInt(0)
				return nil
			},
		})
	})
	return round, err
}

type AgentIdentity struct {
	AgentID     string
	DisplayName string
	Profile     string
	MemoryScope string
	CreatedAt   string
	UpdatedAt   string
}

func (db *DB) UpsertAgentIdentity(ctx context.Context, agentID, displayName, profile, memoryScope string) error {
	if db == nil || agentID == "" {
		return nil
	}
	now := nowString()
	return db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			INSERT INTO agent_identities (workspace_id, agent_id, display_name, profile, memory_scope, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(workspace_id, agent_id) DO UPDATE SET
				display_name = excluded.display_name,
				profile = excluded.profile,
				memory_scope = excluded.memory_scope,
				updated_at = excluded.updated_at
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), agentID, displayName, profile, memoryScope, now, now},
		})
	})
}

func (db *DB) GetAgentIdentity(ctx context.Context, agentID string) (AgentIdentity, error) {
	var a AgentIdentity
	if db == nil || agentID == "" {
		return a, nil
	}
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT agent_id, display_name, profile, memory_scope, created_at, updated_at
			FROM agent_identities WHERE workspace_id = ? AND agent_id = ?
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID(), agentID},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				a = AgentIdentity{
					AgentID:     stmt.ColumnText(0),
					DisplayName: stmt.ColumnText(1),
					Profile:     stmt.ColumnText(2),
					MemoryScope: stmt.ColumnText(3),
					CreatedAt:   stmt.ColumnText(4),
					UpdatedAt:   stmt.ColumnText(5),
				}
				return nil
			},
		})
	})
	return a, err
}

func (db *DB) ListAgentIdentities(ctx context.Context) ([]AgentIdentity, error) {
	if db == nil {
		return nil, nil
	}
	var agents []AgentIdentity
	err := db.WithConn(ctx, func(conn *zsqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `
			SELECT agent_id, display_name, profile, memory_scope, created_at, updated_at
			FROM agent_identities
			WHERE workspace_id = ?
			ORDER BY created_at ASC
		`, &sqlitex.ExecOptions{
			Args: []any{db.WorkspaceID()},
			ResultFunc: func(stmt *zsqlite.Stmt) error {
				agents = append(agents, AgentIdentity{
					AgentID:     stmt.ColumnText(0),
					DisplayName: stmt.ColumnText(1),
					Profile:     stmt.ColumnText(2),
					MemoryScope: stmt.ColumnText(3),
					CreatedAt:   stmt.ColumnText(4),
					UpdatedAt:   stmt.ColumnText(5),
				})
				return nil
			},
		})
	})
	return agents, err
}
