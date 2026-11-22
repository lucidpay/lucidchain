// Package facilitator is a skeleton facilitator for an x402 "exact" scheme
// on a Cosmos SDK chain. See x402-cosmos-exact-spec.md for the normative rules.
//
// The core verify/settle logic here has NO Cosmos SDK imports. Everything that
// touches the SDK or the node sits behind the interfaces in ports.go, so the
// logic can be unit-tested and the adapters written and checked against your
// exact SDK version (v0.55.x) separately.
//
// STATUS: draft. Wire field names follow the x402 v2 style but MUST be checked
// against the current spec before you publish a scheme.
package facilitator

// Scheme and flow identifiers.
const (
	SchemeExact = "exact"

	FlowSend = "send" // payer signs a single bank MsgSend

	TransportInline    = "inline"    // full signed tx inside the payment header
	TransportReference = "reference" // short reference, tx uploaded separately (not implemented)

	MsgSendTypeURL = "/cosmos.bank.v1beta1.MsgSend"
)

// PaymentRequirements is what the resource server advertises in its 402 response.
type PaymentRequirements struct {
	Scheme            string            `json:"scheme"`
	Network           string            `json:"network"` // CAIP-2, e.g. "cosmos:my-testnet-1"
	Amount            string            `json:"amount"`  // integer string in base-denom units
	Asset             string            `json:"asset"`   // base denom, e.g. "ucheck"
	PayTo             string            `json:"payTo"`   // bech32 address
	MaxTimeoutSeconds int64             `json:"maxTimeoutSeconds"`
	Extra             RequirementsExtra `json:"extra"`
}

// RequirementsExtra carries the Cosmos-specific scheme parameters.
type RequirementsExtra struct {
	Flow       string   `json:"flow"`                 // "send"
	KeyTypes   []string `json:"keyTypes,omitempty"`   // accepted pubkey type URLs
	Transports []string `json:"transports,omitempty"` // accepted payload transports
	FeeMode    string   `json:"feeMode,omitempty"`    // "payer" | "sponsored"
	PaymentID  string   `json:"paymentId,omitempty"`  // bound into the tx memo
}

// PaymentPayload is what the client sends back in the payment header.
type PaymentPayload struct {
	X402Version int           `json:"x402Version"`
	Scheme      string        `json:"scheme"`
	Network     string        `json:"network"`
	Payload     CosmosPayload `json:"payload"`
}

// CosmosPayload is the scheme-specific part of the payment payload.
type CosmosPayload struct {
	Transport string `json:"transport"`     // "inline" | "reference"
	KeyType   string `json:"keyType"`       // pubkey type URL the payer signed with
	Tx        string `json:"tx,omitempty"`  // base64 TxRaw (inline)
	Ref       string `json:"ref,omitempty"` // reference id (reference)
}

// VerifyRequest / SettleRequest share one shape.
type VerifyRequest struct {
	X402Version         int                 `json:"x402Version"`
	PaymentPayload      PaymentPayload      `json:"paymentPayload"`
	PaymentRequirements PaymentRequirements `json:"paymentRequirements"`
}

type SettleRequest = VerifyRequest

type VerifyResponse struct {
	IsValid       bool   `json:"isValid"`
	InvalidReason string `json:"invalidReason,omitempty"`
	Payer         string `json:"payer,omitempty"`
}

type SettleResponse struct {
	Success     bool   `json:"success"`
	ErrorReason string `json:"errorReason,omitempty"`
	Payer       string `json:"payer,omitempty"`
	Transaction string `json:"transaction,omitempty"` // tx hash
	Network     string `json:"network,omitempty"`
}

type SupportedKind struct {
	X402Version int               `json:"x402Version"`
	Scheme      string            `json:"scheme"`
	Network     string            `json:"network"`
	Extra       RequirementsExtra `json:"extra"`
}

type SupportedResponse struct {
	Kinds []SupportedKind `json:"kinds"`
}
