package ios

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The flow format's permission names → TCC services (written to the simulator's TCC database, as
// applesimutils does); location goes through simctl privacy. Notifications have no TCC entry:
// their alert shows, and the flow answers it.
var tccServices = map[string]string{
	"calendar": "kTCCServiceCalendar", "camera": "kTCCServiceCamera", "contacts": "kTCCServiceAddressBook",
	"faceid": "kTCCServiceFaceID", "homekit": "kTCCServiceWillow", "medialibrary": "kTCCServiceMediaLibrary",
	"microphone": "kTCCServiceMicrophone", "motion": "kTCCServiceMotion", "photos": "kTCCServicePhotos",
	"reminders": "kTCCServiceReminders", "siri": "kTCCServiceSiri", "speech": "kTCCServiceSpeechRecognition",
	"usertracking": "kTCCServiceUserTracking", "bluetooth": "kTCCServiceBluetoothAlways",
}

// SetPermissions applies a flow's permissions. An empty map grants all (the format's default).
func (d *Driver) SetPermissions(ctx context.Context, appID string, perms map[string]string) error {
	if err := validID(appID); err != nil {
		return err
	}
	if len(perms) == 0 {
		perms = map[string]string{"all": "allow"}
	}
	tcc := map[string]string{}
	location := ""
	if v, ok := perms["all"]; ok {
		v = strings.ToLower(v)
		for _, s := range tccServices {
			tcc[s] = v
		}
		location = map[string]string{"allow": "always", "deny": "never", "unset": "unset"}[v]
	}
	for name, v := range perms {
		name, v = strings.ToLower(name), strings.ToLower(v)
		if name == "all" {
			continue
		}
		if name == "location" {
			location = v
			if v == "allow" {
				location = "always"
			} else if v == "deny" {
				location = "never"
			}
			continue
		}
		if s, ok := tccServices[name]; ok {
			tcc[s] = v
		}
	}
	if len(tcc) > 0 {
		if err := d.writeTCC(ctx, appID, tcc); err != nil {
			return err
		}
	}
	if location == "" {
		return nil
	}
	d.mu.Lock()
	same := d.locationSet[appID] == location
	d.mu.Unlock()
	if same {
		return nil
	}
	var args []string
	switch location {
	case "always":
		args = []string{"grant", "location-always"}
	case "inuse", "whileinuse":
		args = []string{"grant", "location"}
	case "never":
		args = []string{"revoke", "location-always"}
	case "unset":
		args = []string{"reset", "location-always"}
	default:
		return fmt.Errorf("unknown location permission %q", location)
	}
	if _, err := d.simctl(ctx, append(append([]string{"privacy", d.info.Serial}, args...), appID)...); err != nil {
		return err
	}
	// Remember it only once applied, so a failed grant is tried again next time.
	d.mu.Lock()
	if d.locationSet == nil {
		d.locationSet = map[string]string{}
	}
	d.locationSet[appID] = location
	d.mu.Unlock()
	return nil
}

// simDataDir is a simulator's data directory (its "/").
func simDataDir(udid string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Developer/CoreSimulator/Devices", udid, "data")
}

func (d *Driver) tccDB() string {
	return filepath.Join(simDataDir(d.info.Serial), "Library/TCC/TCC.db")
}

// The simulator's configuration-profile settings, where Password AutoFill is allowed or not.
var autoFillPlists = []string{
	"Library/UserConfigurationProfiles/EffectiveUserSettings.plist",
	"Containers/Shared/SystemGroup/systemgroup.com.apple.configurationprofiles/Library/ConfigurationProfiles/UserSettings.plist",
}

// disablePasswordAutoFill turns off Password AutoFill on the simulator (Maestro #1061): its
// Strong Password sheet takes over secure and new-password fields, and typed text goes
// nowhere. TB_IOS_KEEP_AUTOFILL=1 leaves it as it is. Missing files are skipped quietly.
func disablePasswordAutoFill(ctx context.Context, dataDir string) {
	if os.Getenv("TB_IOS_KEEP_AUTOFILL") == "1" {
		return
	}
	const key = "restrictedBool.allowPasswordAutoFill"
	for _, rel := range autoFillPlists {
		f := filepath.Join(dataDir, rel)
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if out, err := exec.CommandContext(ctx, "plutil", "-extract", key+".value", "raw", f).Output(); err == nil &&
			strings.TrimSpace(string(out)) == "false" {
			continue // already off
		}
		// Create the dictionaries if missing (an insert of an existing key fails harmlessly).
		exec.CommandContext(ctx, "plutil", "-insert", "restrictedBool", "-dictionary", f).Run()
		exec.CommandContext(ctx, "plutil", "-insert", key, "-dictionary", f).Run()
		exec.CommandContext(ctx, "plutil", "-replace", key+".value", "-bool", "NO", f).Run()
	}
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (d *Driver) writeTCC(ctx context.Context, appID string, tcc map[string]string) error {
	var sql strings.Builder
	sql.WriteString("BEGIN;")
	services := make([]string, 0, len(tcc))
	for s := range tcc {
		services = append(services, s)
	}
	sort.Strings(services)
	for _, service := range services {
		auth := -1
		switch tcc[service] {
		case "allow":
			auth = 2
		case "deny":
			auth = 0
		case "limited":
			auth = 3
		case "unset":
		default:
			continue
		}
		fmt.Fprintf(&sql, "DELETE FROM access WHERE service=%s AND client=%s AND client_type=0;", sqlQuote(service), sqlQuote(appID))
		if auth >= 0 {
			fmt.Fprintf(&sql, "INSERT INTO access (service, client, client_type, auth_value, auth_reason, auth_version, flags) VALUES (%s, %s, 0, %d, 2, 1, 0);", sqlQuote(service), sqlQuote(appID), auth)
		}
	}
	sql.WriteString("COMMIT;")
	// tccd can hold the database for a while after boot: wait for its lock.
	out, err := exec.CommandContext(ctx, "sqlite3", "-cmd", ".timeout 5000", d.tccDB(), sql.String()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("setting permissions in %s: %v: %s", d.tccDB(), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *Driver) resetTCC(ctx context.Context, appID string) {
	exec.CommandContext(ctx, "sqlite3", "-cmd", ".timeout 5000", d.tccDB(),
		"DELETE FROM access WHERE client="+sqlQuote(appID)+" AND client_type=0;").Run()
}
