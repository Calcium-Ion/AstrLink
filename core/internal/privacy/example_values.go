package privacy

// Payment providers publish card numbers for test payments, and integration
// code and docs are full of them. No account stands behind one, so every
// detector leaves them in place; replacing one only garbles the code an agent
// works on. Addresses on domains reserved for examples are default allowlist
// entries instead, where they stay visible and removable.

// testCardNumbers are the numbers card networks and payment providers such as
// Stripe, Braintree, and Adyen publish for test payments.
var testCardNumbers = map[string]struct{}{
	"4242424242424242": {},
	"4000056655665556": {},
	"4111111111111111": {},
	"4012888888881881": {},
	"4222222222222":    {},
	"4000002500003155": {},
	"4000000000000002": {},
	"4000000000009995": {},
	"5555555555554444": {},
	"2223003122003222": {},
	"5200828282828210": {},
	"5105105105105100": {},
	"5431111111111111": {},
	"5555341244441115": {},
	"378282246310005":  {},
	"371449635398431":  {},
	"378734493671000":  {},
	"370000000000002":  {},
	"6011111111111117": {},
	"6011000990139424": {},
	"6011601160116611": {},
	"3056930009020004": {},
	"36227206271667":   {},
	"30569309025904":   {},
	"38520000023237":   {},
	"3566002020360505": {},
	"3530111333300000": {},
	"6200000000000005": {},
}

// isExampleValue reports a published test card number, however its digits
// are grouped.
func isExampleValue(kind Kind, value string) bool {
	if kind != KindPaymentCard {
		return false
	}
	_, published := testCardNumbers[string(decimalDigits(value))]
	return published
}
