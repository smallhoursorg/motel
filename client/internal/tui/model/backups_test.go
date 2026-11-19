package model_test

import (
	"strings"
	"testing"

	"github.com/smallhoursorg/motel/client/internal/tui/model"
)

func joined(ls []model.Line) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Text() + "\n")
	}
	return b.String()
}

// TestBackupsSays: the backups screen and the box screen's line say the
// box's verdict as the box's, and that it is the box's own claim next to
// it; a could not tell is laid on rows whole, and on the box screen's
// line the answer's age and the way to the screen come before it, so a
// cut at the window's edge cuts neither; a warning's text is not said
// after its level twice; and a refusal, held until the person asks,
// says how to ask.
func TestBackupsSays(t *testing.T) {
	s, f, now := session(t)
	s.Engine.BackupsOpen(true, *now)
	why := "backups are not set up: /etc/motel-backup/repository.env is not there (/etc/motel/backup.env is from before this version and is not read: run `motel-backup setup` as root)"
	f.Body("backups.json", `{"at":"@-2s","healthy":false,"could_not_tell":"`+why+`","apps":[],"running":[],"unrecognised_units":[],"lines":[{"level":"warning","text":"warning: the run said so"}]}`)
	for range 3 {
		step(t, s, f, now)
	}
	if s.BackupsSt == nil {
		t.Fatalf("not read: %v", s.BackupsErr)
	}
	v := s.BackupsViewFit(*now, 60, runes)
	text := joined(v)
	if !strings.Contains(strings.Join(strings.Fields(text), " "), "as root)\"") || !strings.Contains(text, "· the box's claim\n") {
		t.Errorf("at 60 columns the screen says:\n%s", text)
	}
	for _, l := range v {
		if l.Width(runes) > 60 {
			t.Errorf("a row wider than 60: %q", l.Text())
		}
	}
	if strings.Contains(text, "warning: the run said so") || !strings.Contains(text, "warning  the run said so") {
		t.Errorf("a warning's line:\n%s", text)
	}
	box := s.BoxBackupsLine(*now).Cut(60, runes).Text()
	if !strings.HasPrefix(box, "backups     ") || !strings.Contains(box, " ago · :backups · could not tell") {
		t.Errorf("the box screen's line at 60 columns: %q", box)
	}

	// A 403 drops the answer, holds the read, and says ctrl-r asks again.
	*now = now.Add(model.BoxEvery)
	f.Set("backups", &model.Failure{Kind: model.FailForbidden, Message: "deploy.example.com does not allow your key this: it needs the viewer role on the box."})
	for range 3 {
		step(t, s, f, now)
	}
	if text := joined(s.BackupsView(*now)); s.BackupsSt != nil || !strings.Contains(text, "viewer role on the box") || !strings.Contains(text, "ctrl-r asks again") {
		t.Errorf("after a 403: held %v, the screen says:\n%s", s.BackupsSt != nil, text)
	}
}
