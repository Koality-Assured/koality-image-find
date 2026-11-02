package dedup

// DSU implements a high-performance Disjoint Set Union data structure
// with iterative path compression and union-by-rank.
type DSU struct {
	parent []int
	rank   []int
	count  int // Number of disjoint sets
}

// NewDSU initializes a DSU with n elements (0 to n-1).
func NewDSU(n int) *DSU {
	parent := make([]int, n)
	rank := make([]int, n)
	for i := 0; i < n; i++ {
		parent[i] = i
		rank[i] = 0
	}
	return &DSU{
		parent: parent,
		rank:   rank,
		count:  n,
	}
}

// Find returns the canonical representative of the set containing x with two-pass path compression.
func (d *DSU) Find(x int) int {
	root := x
	for root != d.parent[root] {
		root = d.parent[root]
	}

	// Path compression
	curr := x
	for curr != root {
		nxt := d.parent[curr]
		d.parent[curr] = root
		curr = nxt
	}

	return root
}

// Union merges the sets containing x and y. Returns true if sets were merged, false if already in the same set.
func (d *DSU) Union(x, y int) bool {
	rootX := d.Find(x)
	rootY := d.Find(y)

	if rootX == rootY {
		return false
	}

	if d.rank[rootX] < d.rank[rootY] {
		d.parent[rootX] = rootY
	} else if d.rank[rootX] > d.rank[rootY] {
		d.parent[rootY] = rootX
	} else {
		d.parent[rootY] = rootX
		d.rank[rootX]++
	}

	d.count--
	return true
}

// Count returns the current number of disjoint components.
func (d *DSU) Count() int {
	return d.count
}
