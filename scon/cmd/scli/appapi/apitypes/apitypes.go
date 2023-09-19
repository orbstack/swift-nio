package apitypes

type CreateCheckoutSessionRequest struct {
	PriceKey   string            `json:"priceKey"`
	CancelPath string            `json:"cancelPath"`
	Metadata   map[string]string `json:"metadata"`
}

type CreateCheckoutSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type CreateBillingPortalSessionRequest struct {
	ReturnPath string `json:"returnPath"`
}

type CreateBillingPortalSessionResponse struct {
	SessionURL string `json:"sessionUrl"`
}

type GetBillingStatusResponse struct {
	SubscriptionActive bool `json:"subscriptionActive"`

	// admin only
	MaxSeats        int                    `json:"maxSeats"`
	AssignedSeats   int                    `json:"assignedSeats"`
	SeatAssignments []SeatAssignmentRecord `json:"seatAssignments"`
}

type RevokeSeatRequest struct {
	SeatType string `json:"seatType"`
	SeatID   string `json:"seatId"`
}

type SeatType string

const (
	SeatTypeDeviceSerial SeatType = "device_serial"
	SeatTypeUser         SeatType = "user"
)

type SeatAssignmentRecord struct {
	ID       int      `db:"id" json:"id"`
	SeatType SeatType `db:"seat_type" json:"seatType"`
	SeatID   string   `db:"seat_id" json:"seatId"`
	OrgID    string   `db:"org_id" json:"orgId"`
}
