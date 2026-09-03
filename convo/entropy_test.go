package convo

import "testing"

func TestHasCursorEntropy(t *testing.T) {
	accept := []string{
		"call_7f3a9c2e1b4d4e8fa1c2",
		"toolu_01XyZabc123DEF456ghi789JK",
		"fc_68b7d1e2f3a4b5c6d7e8f9a1",
		"msg_68b7d2a0b1c2d3e4f5a6b7c8",
		"rs_68b7d2a0f0e1d2c3b4a59688",
		"chatcmpl-tool-9f2b7c1ea81d",
		"srvtoolu_01AbCdEfGhIj",
		"ts_0123456789abcdef0123456789abcdef",
		"6f1d2c3b-4a59-4e87-9c0d-1e2f3a4b5c6d",
	}
	for _, id := range accept {
		if !HasCursorEntropy(id) {
			t.Errorf("expected %q to pass", id)
		}
	}
	reject := []string{
		"",
		"call_0",
		"call_12",
		"call_00000001",
		"call_12345678",
		"tool_1",
		"toolu_1",
		"aaaaaaaaaaaa",
		"call_aaaabbbb",
		"short",
		"has space in it",
		"tab\tinside_id_value",
		"call_abc",
	}
	for _, id := range reject {
		if HasCursorEntropy(id) {
			t.Errorf("expected %q to be rejected", id)
		}
	}
}
