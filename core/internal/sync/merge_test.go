package sync

import (
	"encoding/json"
	"testing"
	"time"
)

func entity(t *testing.T, at time.Time, document string) Entity {
	t.Helper()
	return Entity{UpdatedAt: at, Document: json.RawMessage(document)}
}

func deleted(at time.Time) Entity {
	return Entity{UpdatedAt: at, Deleted: true}
}

func doc(entities map[string]map[string]Entity, order ...string) Document {
	document := NewDocument("device_a", 1, time.Time{})
	document.Entities = entities
	document.Order = order
	return document
}

func merge(t *testing.T, base, local, remote Document) (Document, []Notice) {
	t.Helper()
	merged, notices, err := Merge(base, local, remote)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return merged, notices
}

func TestMergeTakesTheOnlySideThatChanged(t *testing.T) {
	base := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
	})
	local := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
	})
	remote := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(200, 0), `{"name":"b"}`)},
	})
	merged, notices := merge(t, base, local, remote)
	if len(notices) != 0 {
		t.Fatalf("unexpected notices: %+v", notices)
	}
	got, _ := merged.Get(KindService, "svc_1")
	if string(got.Document) != `{"name":"b"}` {
		t.Fatalf("merged = %s", got.Document)
	}
}

func TestMergeNewEntityFromEitherSide(t *testing.T) {
	base := doc(map[string]map[string]Entity{})
	local := doc(map[string]map[string]Entity{
		KindPolicy: {"pol_1": entity(t, time.Unix(100, 0), `{"k":1}`)},
	})
	remote := doc(map[string]map[string]Entity{
		KindPolicy: {"pol_2": entity(t, time.Unix(100, 0), `{"k":2}`)},
	})
	merged, notices, err := Merge(base, local, remote)
	if err != nil || len(notices) != 0 {
		t.Fatalf("merge = %+v, %v", notices, err)
	}
	if len(merged.Entities[KindPolicy]) != 2 {
		t.Fatalf("entities = %+v", merged.Entities[KindPolicy])
	}
}

// Both sides changed the same entity: the later change wins and the merge is
// reported rather than dropping one side silently.
func TestMergeConflictLaterWins(t *testing.T) {
	base := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
	})
	local := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(300, 0), `{"name":"local"}`)},
	})
	remote := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(200, 0), `{"name":"remote"}`)},
	})
	merged, notices := merge(t, base, local, remote)
	if len(notices) != 1 || notices[0].Resolution != ResolutionLocalWon {
		t.Fatalf("notices = %+v", notices)
	}
	got, _ := merged.Get(KindService, "svc_1")
	if string(got.Document) != `{"name":"local"}` {
		t.Fatalf("merged = %s", got.Document)
	}
}

// A tie keeps this device's copy and reports the conflict, so a repeated merge
// is stable.
func TestMergeTieKeepsLocalAndReports(t *testing.T) {
	at := time.Unix(200, 0)
	base := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
	})
	local := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, at, `{"name":"local"}`)},
	})
	remote := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, at, `{"name":"remote"}`)},
	})
	merged, notices := merge(t, base, local, remote)
	if len(notices) != 1 || notices[0].Resolution != ResolutionTieKeptLocal {
		t.Fatalf("notices = %+v", notices)
	}
	got, _ := merged.Get(KindService, "svc_1")
	if string(got.Document) != `{"name":"local"}` {
		t.Fatalf("merged = %s", got.Document)
	}
}

// A delete and an edit are compared by time, so a later edit beats an earlier
// delete and a later delete beats an earlier edit.
func TestMergeDeleteVersusEdit(t *testing.T) {
	base := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
	})
	// Delete later than the remote edit: the delete wins.
	local := doc(map[string]map[string]Entity{
		KindService: {"svc_1": deleted(time.Unix(300, 0))},
	})
	remote := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(200, 0), `{"name":"b"}`)},
	})
	merged, notices := merge(t, base, local, remote)
	if _, present := merged.Get(KindService, "svc_1"); present {
		t.Fatal("a later delete should remove the entity")
	}
	if len(notices) != 1 || notices[0].Resolution != ResolutionRemoved {
		t.Fatalf("notices = %+v", notices)
	}
	// Edit later than the remote delete: the edit wins and the entity stays.
	local = doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(300, 0), `{"name":"edited"}`)},
	})
	remote = doc(map[string]map[string]Entity{
		KindService: {"svc_1": deleted(time.Unix(200, 0))},
	})
	merged, _ = merge(t, base, local, remote)
	if got, present := merged.Get(KindService, "svc_1"); !present || string(got.Document) != `{"name":"edited"}` {
		t.Fatalf("an earlier delete beat a later edit: %+v", got)
	}
}

// A deletion propagates: a device that still has the entity removes it.
func TestMergeDeletePropagates(t *testing.T) {
	base := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
	})
	local := doc(map[string]map[string]Entity{
		KindService: {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
	})
	remote := doc(map[string]map[string]Entity{
		KindService: {"svc_1": deleted(time.Unix(200, 0))},
	})
	merged, notices := merge(t, base, local, remote)
	if _, present := merged.Get(KindService, "svc_1"); present {
		t.Fatal("the deletion did not propagate")
	}
	if len(notices) != 0 {
		t.Fatalf("a propagated delete is not a conflict: %+v", notices)
	}
}

func TestMergeOrderKeepsEveryService(t *testing.T) {
	base := doc(nil, "b", "a")
	local := doc(nil, "a", "b", "c")
	remote := doc(nil, "b", "a")
	merged, _ := merge(t, base, local, remote)
	if len(merged.Order) != 3 {
		t.Fatalf("order = %v", merged.Order)
	}
	// Both sides changed the order: the remote order leads, local-only ids
	// follow so nothing drops out.
	remote = doc(nil, "c", "b", "a")
	merged, _ = merge(t, base, local, remote)
	if len(merged.Order) != 3 || merged.Order[0] != "c" {
		t.Fatalf("order = %v", merged.Order)
	}
}

func TestDecodeRejectsFutureVersion(t *testing.T) {
	encoded, err := NewDocument("device_a", 1, time.Unix(0, 0)).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(encoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	raw["version"] = PackageVersion + 1
	mutated, _ := json.Marshal(raw)
	if _, err := Decode(mutated); err == nil {
		t.Fatal("a future package version was accepted")
	}
}

// A credential travels inside the package as a secret, and a device that has
// none receives it whole.
func TestMergeCarriesCredentials(t *testing.T) {
	base := doc(map[string]map[string]Entity{})
	local := doc(map[string]map[string]Entity{})
	remote := doc(map[string]map[string]Entity{
		KindService:           {"svc_1": entity(t, time.Unix(100, 0), `{"name":"a"}`)},
		KindServiceCredential: {"svc_1": {UpdatedAt: time.Unix(100, 0), Secret: "c2VjcmV0"}},
	})
	merged, _, err := Merge(base, local, remote)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	credential, present := merged.Get(KindServiceCredential, "svc_1")
	if !present || credential.Secret != "c2VjcmV0" {
		t.Fatalf("credential = %+v, %v", credential, present)
	}
}
