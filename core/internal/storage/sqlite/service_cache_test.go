package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestServiceDocumentCacheSeesExternalChangesAndDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-cache.db")
	store := openUnseededTestStore(t, path)
	t.Cleanup(func() { _ = store.Close() })
	created, err := store.CreateService(t.Context(), pathTestService("service_cached"), storage.CredentialMutation{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.GetService(t.Context(), created.Service.ID)
	if err != nil {
		t.Fatal(err)
	}
	first.Service.Models[0] = "caller-only"
	first.Service.HTTP.BaseURL = "https://caller-only.example"
	page, err := store.ListServices(t.Context(), storage.ServiceListOptions{})
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].Service, created.Service) {
		t.Fatalf("caller mutated cached service: page=%v error=%v", page, err)
	}
	external, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = external.Close() })
	changed := created.Service
	changed.Name = "updated outside store"
	changed.Enabled = false
	document, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := external.Exec(`UPDATE services SET document_json = ? WHERE id = ?`, string(document), changed.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.GetService(t.Context(), changed.ID)
	if err != nil || fresh.Service.Name != changed.Name || fresh.Service.Enabled || fresh.ETag == first.ETag {
		t.Fatalf("external update was stale: record=%v error=%v", fresh, err)
	}
	enabled := true
	page, err = store.ListServices(t.Context(), storage.ServiceListOptions{Enabled: &enabled})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("updated enabled filter was stale: page=%v error=%v", page, err)
	}
	if _, err := external.Exec(`UPDATE services SET document_json = '{}' WHERE id = ?`, changed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetService(t.Context(), changed.ID); !errors.Is(err, storage.ErrInvalidRecord) {
		t.Fatalf("invalid new document used cached value: %v", err)
	}
	if _, err := external.Exec(`DELETE FROM services WHERE id = ?`, changed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetService(t.Context(), changed.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted service used cached value: %v", err)
	}
}

func TestCachedServiceCloneOwnsEveryMutableField(t *testing.T) {
	// Populate every exported field reflectively so future pointer, slice or
	// map fields fail this test until the cache clone handles them too.
	var original, expected storage.ServiceRecord
	fillServiceCloneFixture(reflect.ValueOf(&original).Elem())
	fillServiceCloneFixture(reflect.ValueOf(&expected).Elem())
	cloned := cloneCachedServiceRecord(original)
	if !reflect.DeepEqual(original, cloned) {
		t.Fatal("clone changed document values")
	}
	clearServiceCloneFixture(reflect.ValueOf(&cloned).Elem())
	if !reflect.DeepEqual(original, expected) {
		t.Fatal("mutating returned record changed a cached reference")
	}
}

var serviceCloneTimeType = reflect.TypeFor[time.Time]()

func fillServiceCloneFixture(value reflect.Value) {
	if value.Type() == serviceCloneTimeType {
		value.Set(reflect.ValueOf(time.Unix(100, 0).UTC()))
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fillServiceCloneFixture(value.Elem())
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			fillServiceCloneFixture(value.Field(index))
		}
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		fillServiceCloneFixture(value.Index(0))
	case reflect.Map:
		value.Set(reflect.MakeMap(value.Type()))
		key, item := reflect.New(value.Type().Key()).Elem(), reflect.New(value.Type().Elem()).Elem()
		fillServiceCloneFixture(key)
		fillServiceCloneFixture(item)
		value.SetMapIndex(key, item)
	case reflect.String:
		value.SetString("fixture")
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(1)
	default:
		panic("extend service clone fixture for " + value.Type().String())
	}
}

func clearServiceCloneFixture(value reflect.Value) {
	if value.Type() == serviceCloneTimeType {
		value.SetZero()
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		clearServiceCloneFixture(value.Elem())
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			clearServiceCloneFixture(value.Field(index))
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			clearServiceCloneFixture(value.Index(index))
		}
	case reflect.Map:
		value.Clear()
	default:
		value.SetZero()
	}
}

func TestServiceDocumentCacheBoundsEntriesAndBytes(t *testing.T) {
	for _, padding := range []int{0, 128 << 10} {
		cache := serviceDocumentCache{}
		for index := 0; index <= maxServiceCacheEntries; index++ {
			service := pathTestService(contract.ServiceID(fmt.Sprintf("service_%03d", index)))
			document, err := json.Marshal(service)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cache.decode(string(service.ID), strings.Repeat(" ", padding)+string(document)); err != nil {
				t.Fatal(err)
			}
			if len(cache.entries) > maxServiceCacheEntries || cache.bytes > maxServiceCacheBytes {
				t.Fatalf("cache unbounded: entries=%d bytes=%d", len(cache.entries), cache.bytes)
			}
		}
		if _, ok := cache.entries["service_000"]; ok {
			t.Fatal("oldest entry was not evicted")
		}
	}
}

func BenchmarkServiceDocumentDecode(b *testing.B) {
	service := pathTestService("service_bench")
	service.Models = make([]string, 2000)
	for index := range service.Models {
		service.Models[index] = fmt.Sprintf("model-%04d", index)
	}
	document, err := encodeService(service)
	if err != nil {
		b.Fatal(err)
	}
	text := string(document)
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			cache := serviceDocumentCache{}
			if _, err := cache.decode(string(service.ID), text); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var err error
				if cached {
					_, err = cache.decode(string(service.ID), text)
				} else {
					_, err = decodeServiceRecord(string(service.ID), document)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
