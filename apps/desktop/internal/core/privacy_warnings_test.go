package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

type warningEvent struct {
	sequence   uint64
	notifiable bool
	kinds      []string
}

func pageOf(t *testing.T, epoch string, events ...warningEvent) warningPage {
	t.Helper()
	encoded := make([]map[string]any, len(events))
	for i, event := range events {
		kinds := make([]map[string]any, len(event.kinds))
		for j, kind := range event.kinds {
			kinds[j] = map[string]any{"kind": kind, "count": 1}
		}
		encoded[i] = map[string]any{
			"sequence":   event.sequence,
			"at":         "2026-10-12T10:00:00Z",
			"request_id": fmt.Sprintf("req_%d", event.sequence),
			"kinds":      kinds,
			"notifiable": event.notifiable,
		}
	}
	body, _ := json.Marshal(map[string]any{"epoch": epoch, "events": encoded})
	page, err := parseWarningPage(body)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func advanced(epoch string, sequence uint64) WarningCursor {
	return WarningCursor{epoch: &epoch, sequence: sequence}
}

func advance(cursor WarningCursor, kinds ...string) warningStep {
	if kinds == nil {
		kinds = []string{}
	}
	return warningStep{cursor: cursor, kinds: kinds}
}

func TestWarningSteps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cursor WarningCursor
		page   warningPage
		want   warningStep
	}{
		{"a burst becomes one notification naming every new kind", WarningCursor{}, pageOf(t, "a",
			warningEvent{1, true, []string{"common_secret"}},
			// The same secret resent with the history: already notified.
			warningEvent{2, false, []string{"common_secret"}},
			warningEvent{3, true, []string{"email", "common_secret"}},
		), advance(advanced("a", 3), "email", "common_secret")},
		{"warnings without a new value only move the cursor", advanced("a", 3), pageOf(t, "a",
			warningEvent{4, false, []string{"email"}}, warningEvent{5, false, []string{"email"}},
		), advance(advanced("a", 5))},
		{"an empty page keeps the cursor", advanced("a", 5), pageOf(t, "a"), advance(advanced("a", 5))},
		// The new Core numbers from 1 again, so its first warnings sit below
		// the old cursor and would be lost if the cursor were kept.
		{"a restarted core is read again", advanced("a", 9), pageOf(t, "b", warningEvent{1, true, []string{"phone"}}),
			warningStep{restart: true}},
		{"a restarted core from the start", WarningCursor{}, pageOf(t, "b", warningEvent{1, true, []string{"phone"}}),
			advance(advanced("b", 1), "phone")},
		{"warnings already read are not shown again", advanced("a", 2), pageOf(t, "a",
			warningEvent{2, true, []string{"email"}}, warningEvent{3, true, []string{"url"}},
		), advance(advanced("a", 3), "url")},
		{"kinds from a newer core read as one other kind", WarningCursor{}, pageOf(t, "a",
			warningEvent{1, true, []string{"passport", "email", "tax_id"}},
		), advance(advanced("a", 1), "email", "unknown")},
	} {
		if got := stepWarnings(tc.cursor, tc.page); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestTheNotificationNamesKindsAndTheSaferActions(t *testing.T) {
	title, body := warningNotificationText(prefs.LocaleZhCN, []string{"email", "common_secret"})
	if title != "已按原文发给模型：邮箱、常见密钥" {
		t.Errorf("zh title = %q", title)
	}
	for _, action := range []string{"「警告并继续」", "「脱敏后继续」", "「阻止请求」"} {
		if !strings.Contains(body, action) {
			t.Errorf("zh body %q lacks %s", body, action)
		}
	}
	title, body = warningNotificationText(prefs.LocaleEn, []string{"common_secret"})
	if title != "Sent to the model as is: Common secrets" {
		t.Errorf("en title = %q", title)
	}
	for _, action := range []string{"“Redact and continue”", "“Block the request”"} {
		if !strings.Contains(body, action) {
			t.Errorf("en body %q lacks %s", body, action)
		}
	}
	if title, _ := warningNotificationText(prefs.LocaleEn, []string{"unknown"}); title != "Sent to the model as is: Other" {
		t.Errorf("unknown title = %q", title)
	}
}

func TestAPageWithoutEventsParsesAsEmpty(t *testing.T) {
	page, err := parseWarningPage([]byte(`{"epoch":"a"}`))
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if _, err := parseWarningPage([]byte("not json")); err == nil {
		t.Fatal("invalid JSON parsed")
	}
}

// pagedCaller answers each feed read with the next page.
type pagedCaller struct {
	pages []string
	paths []string
}

func (c *pagedCaller) Call(_ context.Context, req control.Request) (control.Response, error) {
	c.paths = append(c.paths, req.Path)
	body := c.pages[0]
	c.pages = c.pages[1:]
	return control.Response{Status: http.StatusOK, Body: []byte(body)}, nil
}

func TestPollRereadsARestartedCoreFromItsFirstWarning(t *testing.T) {
	caller := &pagedCaller{pages: []string{
		`{"epoch":"b","events":[{"sequence":1,"kinds":[{"kind":"phone"}],"notifiable":true}]}`,
		`{"epoch":"b","events":[{"sequence":1,"kinds":[{"kind":"phone"}],"notifiable":true}]}`,
	}}
	cursor := advanced("a", 9)
	var titles []string
	PollPrivacyWarnings(context.Background(), caller, &cursor, prefs.LocaleEn, func(title, _ string) {
		titles = append(titles, title)
	})
	if !reflect.DeepEqual(caller.paths, []string{PrivacyWarningsPath + "?since=9", PrivacyWarningsPath + "?since=0"}) {
		t.Fatalf("paths = %v", caller.paths)
	}
	if !reflect.DeepEqual(cursor, advanced("b", 1)) || len(titles) != 1 {
		t.Fatalf("cursor = %+v, titles = %v", cursor, titles)
	}
}
