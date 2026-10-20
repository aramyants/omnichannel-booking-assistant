package benefit

// CertificateType is a prepaid product's catalog description, not a quote for a
// sale or proof that the product can pay for an arbitrary service.
type CertificateType struct {
	ID                string  `json:"id"`
	Title             string  `json:"title"`
	Value             float64 `json:"value"`
	MultipleUse       bool    `json:"multipleUse"`
	Restriction       int     `json:"restriction"`
	ExpiryType        int     `json:"expiryType"`
	ExpiryDate        string  `json:"expiryDate,omitempty"`
	ExpiryPeriod      int     `json:"expiryPeriod"`
	ExpiryUnit        int     `json:"expiryUnit"`
	OnlineSaleEnabled bool    `json:"onlineSaleEnabled"`
}

// Certificate is private: Number must remain inside authenticated server calls.
// Browser responses use a separate projection with a masked reference.
type Certificate struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Balance      float64 `json:"balance"`
	Number       string  `json:"number"`
	MaskedNumber string  `json:"maskedNumber"`
	ExpiresAt    string  `json:"expiresAt,omitempty"`
	Status       string  `json:"status"`
}
