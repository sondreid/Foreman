package core

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Participant is one line of the journal: what new was called with, kept so
// restore can bring the participant back after the tmux server has died
// (power-down). The journal is foreman's one file, and a deliberate exception
// to "never duplicate tmux state": while the server runs, tmux is
// authoritative and nothing reads the journal; only restore does, and only
// new, done, rename write it. ID is exported into the window as FM_SESSION so
// a role's cmd can resume by it.
type Participant struct {
	ID      string
	Project string
	Name    string
	Dir     string
	Created int64
}

// StateDir is where foreman keeps files that must outlive the tmux server:
// $XDG_STATE_HOME/foreman, else ~/.local/state/foreman (the explorer's log
// lives there too).
func StateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "foreman")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "foreman")
}

func journalPath(id string) string {
	return filepath.Join(StateDir(), "participants", id)
}

// NewSessionID mints a random v4 UUID — valid for claude --session-id.
func NewSessionID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RecordParticipant writes (or rewrites) the participant's journal entry.
func RecordParticipant(p Participant) error {
	path := journalPath(p.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	line := strings.Join([]string{p.Project, p.Name, p.Dir, strconv.FormatInt(p.Created, 10)}, "\t")
	return os.WriteFile(path, []byte(line+"\n"), 0o644)
}

// Participants reads the journal, oldest first. No journal means none.
func Participants() ([]Participant, error) {
	entries, err := os.ReadDir(filepath.Join(StateDir(), "participants"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ps []Participant
	for _, e := range entries {
		data, err := os.ReadFile(journalPath(e.Name()))
		if err != nil {
			continue
		}
		f := strings.Split(strings.TrimSuffix(string(data), "\n"), "\t")
		if len(f) != 4 {
			continue
		}
		created, _ := strconv.ParseInt(f[3], 10, 64)
		ps = append(ps, Participant{ID: e.Name(), Project: f[0], Name: f[1], Dir: f[2], Created: created})
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Created < ps[j].Created })
	return ps, nil
}

func ForgetParticipant(id string) {
	os.Remove(journalPath(id))
}

// ForgetParticipants drops the project's entries — one name, or all of them
// when name is empty.
func ForgetParticipants(project, name string) {
	ps, _ := Participants()
	for _, p := range ps {
		if p.Project == project && (name == "" || p.Name == name) {
			ForgetParticipant(p.ID)
		}
	}
}

// RenameParticipants moves a project's entries to its new name.
func RenameParticipants(old, newName string) error {
	ps, err := Participants()
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Project == old {
			p.Project = newName
			if err := RecordParticipant(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// ServerStart is when the running tmux server started, 0 when there is
// none. Restore uses it to tell a participant the server took down with it
// from one closed on purpose.
func ServerStart() int64 {
	out, err := Tmux("display-message", "-p", "#{start_time}")
	if err != nil {
		return 0
	}
	t, _ := strconv.ParseInt(out, 10, 64)
	return t
}

// Survived reports whether the entry predates the running tmux server (or
// there is none): its window, if missing, went down with a server, not by
// anyone's hand.
func (p Participant) Survived(serverStart int64) bool {
	return serverStart == 0 || p.Created < serverStart
}

// PlanRestore sorts one project's journal entries: those to recreate, and
// those to forget. A survivor whose window is missing is restored; an entry
// made under this server whose window is gone was closed deliberately, so
// it is forgotten. A live window is left alone — unless a survivor carries
// its name, in which case that older entry is the stale one. Two survivors
// sharing a name restore the newest only.
func PlanRestore(entries []Participant, live map[string]bool, serverStart int64) (restore, stale []Participant) {
	slot := map[string]int{}
	for _, p := range entries {
		survived := p.Survived(serverStart)
		switch {
		case live[p.Name] && !survived:
		case live[p.Name] || !survived:
			stale = append(stale, p)
		default:
			if i, dup := slot[p.Name]; dup {
				stale = append(stale, restore[i])
				restore[i] = p
				continue
			}
			slot[p.Name] = len(restore)
			restore = append(restore, p)
		}
	}
	return restore, stale
}
