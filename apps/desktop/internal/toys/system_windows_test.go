package toys

import "testing"

func TestAClickOnThePetDoesNotActivateItsWindow(t *testing.T) {
	if r, ok := mouseActivate(wmMouseActivate); !ok || r != maNoActivate {
		t.Fatal(r, ok)
	}
	if _, ok := mouseActivate(0x0201); ok { // WM_LBUTTONDOWN goes to the page
		t.Fatal("the click itself was eaten")
	}
	if noActivateStyle(0x00080000)&wsExNoActivate == 0 || noActivateStyle(0x00080000)&0x00080000 == 0 {
		t.Fatal("WS_EX_NOACTIVATE replaced the other styles")
	}
	if gwlExStyle != ^uintptr(0)-19 {
		t.Fatal("GWL_EXSTYLE is not -20")
	}
}
