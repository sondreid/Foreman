package core

import "fmt"

// RenameProject renames a project everywhere its name lives: the tmux
// session (when running) and the [project.name] config section (when
// configured). Session and window options (@fm_priority, @fm_role, state
// memory) ride along with the session, but the badge text, the name-derived
// color, the ticker argument and @fm_project all embed the name, so styling
// is re-stamped. Renaming a dormant project is purely the config edit.
func RenameProject(old, newName string) error {
	if err := ValidateProjectName(newName); err != nil {
		return err
	}
	if newName == old {
		return fmt.Errorf("%s is already named %s", old, newName)
	}
	live := SessionExists(old)
	cfg := LoadConfig()
	_, configured := cfg.Projects[old]
	if !live && !configured {
		return fmt.Errorf("no project %s", old)
	}
	if SessionExists(newName) {
		return fmt.Errorf("a project %s is already running", newName)
	}
	if _, taken := cfg.Projects[newName]; taken {
		return fmt.Errorf("project %s already exists in %s", newName, ConfigPath())
	}
	if live {
		if _, err := Tmux("rename-session", "-t", "="+SessionName(old), SessionName(newName)); err != nil {
			return err
		}
		StyleSession(newName)
	}
	if _, err := RenameProjectInConfig(old, newName); err != nil {
		if live {
			return fmt.Errorf("session renamed, but updating %s failed: %w", ConfigPath(), err)
		}
		return err
	}
	if err := RenameParticipants(old, newName); err != nil {
		return fmt.Errorf("renamed, but the restore journal in %s was not: %w", StateDir(), err)
	}
	return nil
}
