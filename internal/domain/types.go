package domain

import "time"

// Clock abstracts time access so that services and schedulers can be tested
// deterministically without relying on wall-clock time.
type Clock interface {
	Now() time.Time
}

// RealClock returns the actual wall-clock time.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// CargoType categorises goods for capacity and stowage decisions.
type CargoType string

const (
	CargoBattery      CargoType = "battery"      // power batteries / energy storage cabinets
	CargoDangerous    CargoType = "dangerous"    // other dangerous goods
	CargoPhotovoltaic CargoType = "photovoltaic" // PV modules – general stowage
	CargoGeneral      CargoType = "general"      // ordinary general cargo
)

// IsDangerous reports whether the cargo type requires DG stowage and
// documentation. Batteries and dangerous goods are treated as dangerous;
// photovoltaic modules and general cargo are not.
func (c CargoType) IsDangerous() bool {
	return c == CargoBattery || c == CargoDangerous
}

// DestinationPort is one of the Arctic Express European discharge ports.
type DestinationPort string

const (
	PortRotterdam DestinationPort = "Rotterdam"
	PortHamburg   DestinationPort = "Hamburg"
	PortGdynia    DestinationPort = "Gdynia"
)

// AllPorts lists every supported destination port.
var AllPorts = []DestinationPort{PortRotterdam, PortHamburg, PortGdynia}
