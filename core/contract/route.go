package contract

// RouteID identifies a retired route (ADR 0006). It remains so historical
// request records keep their route_id; new records leave it null.
type RouteID string

const AstrLinkAutoModelID = "astrlink/auto"
const AstrLinkTextClassificationV1 = "astrlink-text-v1"
