package factory

import (
	"time"

	"github.com/NamanBalaji/tdm/pkg/protocol"
)

// ProtocolBuilder defines how to construct a new protocol instance.
// It includes both the construction function and priority information
// for protocol selection.
type ProtocolBuilder struct {
	// Builder is a function that creates a new protocol instance
	Builder func() protocol.Protocol

	// Priority determines which protocol to use when multiple protocols
	// support the same URL. Higher numbers indicate higher priority.
	Priority int
}

// FactoryOptions configures the behavior of the protocol factory.
type FactoryOptions struct {
	// DefaultTimeout is the default timeout for protocol operations
	DefaultTimeout time.Duration

	// AllowOverwrite determines if existing protocols can be overwritten
	AllowOverwrite bool

	// ValidateOnRegister enables protocol validation during registration
	ValidateOnRegister bool
}

// DefaultFactoryOptions provides sensible defaults
var DefaultFactoryOptions = FactoryOptions{
	DefaultTimeout:     30 * time.Second,
	AllowOverwrite:     false,
	ValidateOnRegister: true,
}
