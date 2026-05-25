package peripheral

// Snapshotable is the contract peripherals implement so the reverse-step
// ring can round-trip their state alongside CPU + RAM. Implementations
// must:
//
//   - Snapshot() returns a copy that's safe to retain after the
//     peripheral keeps mutating.
//   - Restore(state) replaces the peripheral's state with the supplied
//     bytes. Tolerant of zero-length / malformed input (clears).
//
// Wire format is opaque to the caller — only the same peripheral type
// will ever decode bytes it produced. The CPU side keys snapshots by
// peripheral identity, not by serialised contents.
type Snapshotable interface {
	Snapshot() []byte
	Restore(state []byte)
}
