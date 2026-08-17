package domain

import "time"

// Cargo describes the physical goods being shipped in a single booking.
type Cargo struct {
	Description string          `json:"description"`
	Type        CargoType       `json:"type"`
	WeightKg    float64         `json:"weight_kg"`
	UNNumber    string          `json:"un_number"`
	Destination DestinationPort `json:"destination"`
}

// DangerousGoodsCertificate is the "危包证" uploaded by the forwarder for
// battery and dangerous cargo. It must be valid at the time of customs
// release.
type DangerousGoodsCertificate struct {
	ID           string    `json:"id"`
	UNNumber     string    `json:"un_number"`
	HazardClass  string    `json:"hazard_class"`
	PackingGroup string    `json:"packing_group"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	FilePath     string    `json:"file_path"`
}

// IsValidAt reports whether the certificate covers the given instant.
func (c *DangerousGoodsCertificate) IsValidAt(t time.Time) bool {
	if c == nil {
		return false
	}
	return !t.Before(c.IssuedAt) && !t.After(c.ExpiresAt)
}

// PackingItem is a single line in a packing list ("装箱清单").
type PackingItem struct {
	Description string  `json:"description"`
	Quantity    int     `json:"quantity"`
	WeightKg    float64 `json:"weight_kg"`
}

// PackingList is the forwarder-supplied manifest of container contents.
type PackingList struct {
	Items []PackingItem `json:"items"`
}

// TotalWeight returns the aggregate weight of all packing-list items.
func (p *PackingList) TotalWeight() float64 {
	if p == nil {
		return 0
	}
	total := 0.0
	for _, item := range p.Items {
		total += item.WeightKg * float64(item.Quantity)
	}
	return total
}
