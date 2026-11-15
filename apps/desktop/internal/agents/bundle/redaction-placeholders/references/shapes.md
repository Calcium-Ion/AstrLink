# Placeholder shapes

In the examples, `…` stands for a lowercase hex suffix: 16 digits in a marker
and 12 in a natural stand-in. The suffix is derived from the value, so within
one request a value keeps its placeholder and a different value gets a different
one.

| Kind          | Marker                       | Natural stand-in              |
| ------------- | ---------------------------- | ----------------------------- |
| Email address | `<PRIVATE_EMAIL_…>`          | `redacted-…@private.invalid`  |
| URL           | `<PRIVATE_URL_…>`            | `https://private.invalid/r/…` |
| IP address    | `<PRIVATE_IP_ADDRESS_…>`     | `192.0.2.17`, `203.0.113.9`   |
| Phone number  | `<PRIVATE_PHONE_…>`          | none                          |
| Payment card  | `<PRIVATE_PAYMENT_CARD_…>`   | none                          |
| Bank account  | `<PRIVATE_ACCOUNT_NUMBER_…>` | none                          |
| Secret        | `<SECRET_…>`                 | none                          |
| Person name   | `<PRIVATE_PERSON_…>`         | none                          |
| Address       | `<PRIVATE_ADDRESS_…>`        | none                          |
| Date          | `<PRIVATE_DATE_…>`           | none                          |

## Markers

- The angle brackets belong to the marker. Copy `<`, the name, the suffix, and
  `>` together.
- Secrets, phone numbers, card and account numbers, person names, addresses, and
  dates are always markers.
- A secret marker can cover a key name together with its value, for example a
  whole `password=…` assignment. Keep the marker where it is and edit around it.

## Natural stand-ins

- Each one is a syntactically valid value of its type, drawn from ranges that
  are reserved and can never be real: the `.invalid` top-level domain and the
  RFC 5737 and RFC 3849 documentation address blocks.
- An IPv4 original becomes an IPv4 stand-in and an IPv6 original becomes an IPv6
  stand-in such as `2001:db8::3f2a:91c0:7b4e`.
- A kind set to natural stand-ins falls back to a marker when its reserved range
  runs out, so both shapes can appear for the same kind.
