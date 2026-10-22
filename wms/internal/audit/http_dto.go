package audit

import (
	"github.com/google/uuid"

	"wms/internal/signedaction"
)

// productAuditView is the audit record of one product: every submitted signed
// action with its salt and the recomputed details (camelCase for the frontend).
type productAuditView struct {
	ProductID string       `json:"productId"`
	ItemID    string       `json:"itemId"`
	Actions   []actionView `json:"actions"`
}

type actionView struct {
	ActionID     string       `json:"actionId"`
	OperationID  string       `json:"operationId"`
	PartIndex    int16        `json:"partIndex"`
	ActionType   uint8        `json:"actionType"`
	ItemIDs      []string     `json:"itemIds"`
	Salt         string       `json:"salt"`
	SubmittedAt  string       `json:"submittedAt"`
	OperatorID   string       `json:"operatorId"`
	Operator     string       `json:"operator"`
	KeyOwnerID   string       `json:"keyOwnerId"`
	OrgID        string       `json:"orgId"`
	OrgName      string       `json:"orgName"`
	KeyID        string       `json:"keyId"`
	ChainStatus  string       `json:"chainStatus"`
	RejectReason *int16       `json:"rejectReason"`
	Details      *detailsView `json:"details"`
	DetailsError *string      `json:"detailsError"`
}

// detailsView mirrors Details; fields an action type does not carry are omitted
// so the auditor sees exactly the detail set the action was signed over.
type detailsView struct {
	CargoplaceID        *string   `json:"cargoplaceId,omitempty"`
	StorageBinID        *string   `json:"storageBinId,omitempty"`
	ShippingBufferBinID *string   `json:"shippingBufferBinId,omitempty"`
	DispatchID          *string   `json:"dispatchId,omitempty"`
	OrderIDs            *[]string `json:"orderIds,omitempty"`
	OperatorID          string    `json:"operatorId"`
	OccurredAtMs        uint64    `json:"occurredAtMs"`
}

// newDetailsView maps the recomputed details by action type: receive carries the
// cargoplace, putaway the storage bin, pick the shipping buffer bin and orders,
// ship all of the dispatch ones.
func newDetailsView(d Details) *detailsView {
	v := &detailsView{
		OperatorID:   d.OperatorID.String(),
		OccurredAtMs: d.OccurredAtMs,
	}
	switch d.ActionType {
	case signedaction.TypeReceive:
		v.CargoplaceID = uuidPtr(d.CargoplaceID)
	case signedaction.TypePutAway:
		v.StorageBinID = uuidPtr(d.StorageBinID)
	case signedaction.TypePick:
		v.ShippingBufferBinID = uuidPtr(d.ShippingBufferBinID)
		v.OrderIDs = orderIDsPtr(d.OrderIDs)
	case signedaction.TypeShip:
		v.DispatchID = uuidPtr(d.DispatchID)
		v.ShippingBufferBinID = uuidPtr(d.ShippingBufferBinID)
		v.OrderIDs = orderIDsPtr(d.OrderIDs)
	}
	return v
}

// uuidPtr renders the uuid or nil when it is zero, so unused detail fields are
// omitted from the JSON.
func uuidPtr(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	s := id.String()
	return &s
}

// orderIDsPtr renders the orders of pick and ship actions; the pointer keeps
// the field pick/ship-only while an empty set still serializes as [].
func orderIDsPtr(ids []uuid.UUID) *[]string {
	out := uuidStrings(ids)
	return &out
}

// uuidStrings renders the uuids in stored order; the slice is never nil so an
// empty set serializes as [].
func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
