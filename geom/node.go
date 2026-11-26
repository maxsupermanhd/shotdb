package geom

// CollisionNode behaviour flags.
const (
	FlagTraceable = 0x1
	FlagPhys      = 0x2
	FlagSolid     = 0x4
)

// Node is one collision node: for a vehicle, one damage part. Vertices are in
// the unit's native frame (+x forward, +y up, +z right), in metres.
type Node struct {
	Name  string
	Flags uint32 // behaviour flags; FlagSolid marks a part charged by its chord
	Verts [][3]float32
	Tris  [][3]uint32
}
