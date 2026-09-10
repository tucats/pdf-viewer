package jpx

// This file implements JPEG 2000's "tag tree" coding (ITU-T T.800
// §B.10.2), the structure a packet header (packetheader.go) uses to
// compactly signal two per-code-block facts without spending a bit on
// every code-block in a precinct every single layer: whether a
// code-block is newly included in this layer's data (tagTreeInclusion
// below), and, the first time a code-block is included, how many of its
// most-significant bit-planes are entirely zero (tagTreeZeroBitPlanes).
//
// A tag tree is a quad-tree built over a precinct's code-block grid:
// level 0 has one node per code-block, and each coarser level halves
// both dimensions (rounding up) until reaching a single root node. Each
// node's value is defined as the minimum of its children's values - so
// a coarse ancestor node already answers "is every code-block under me
// at least this far along?" for its whole subtree at once. Decoding
// exploits that: once an ancestor's value is confirmed, sibling
// code-blocks sharing that ancestor never need to re-decode it, only
// whatever is specific to their own finer path below it. This file's
// two tree types keep that shared per-node state (item.go's precinctInfo
// holds one instance of each per precinct), not a caller-visible object
// rebuilt from scratch on every leaf lookup.
//
// # Two trees, two questions
//
// tagTreeZeroBitPlanes answers "what is codeblock (i,j)'s exact value?"
// once, ever (the zero-bit-plane count cannot change once a code-block
// is first included, so nothing about it needs revisiting layer to
// layer).
//
// tagTreeInclusion instead answers a repeated, layer-indexed question:
// "has codeblock (i,j) become included by layer L?" - asked again for
// every later layer until the answer is finally yes. Its node values
// hold a lower bound on "the first layer this subtree includes anything
// in", and (critically) once a node's subtree is confirmed included by
// some layer L, it is included by every later layer too (inclusion only
// turns on, never off), so that node is marked permanently settled and
// is never decoded again - this is the "settled" flag below, distinct
// from a node whose lower bound has merely been raised without yet being
// confirmed.
//
// # Provenance
//
// T.800's own description of tag-tree coding is dense enough that this
// file's exact state-machine shape (what "settled" means, when a node's
// value propagates to its children, why the inclusion tree needs a
// permanent-settlement flag that the simpler zero-bit-plane tree does
// not) is cross-checked line-by-line against Mozilla's pdf.js
// (jpx.js's TagTree/InclusionTree classes, Apache License 2.0 - one of
// very few independent, real-world-proven implementations of this exact
// corner of the standard), the same "write from the spec, cross-check
// against a proven implementation" approach docs/PLAN2.md's Phase 13
// entry used for mesh shading's bit-packing rules. It is restructured
// here into explicit Go fields (a "settled" bool slice) rather than
// pdf.js's JavaScript sentinel values (undefined, and the reserved
// integer 0xff), and split into two named types rather than duplicating
// tree-walking logic, but the decode procedure itself - which node gets
// visited in which order, and what each of readBits' 0/1 outcomes means
// at that node - is unchanged.

// tagTreeLevel is one level of either tree type: width/height give this
// level's node grid size, and index is scratch space (which flat node
// index the level's fields below currently refer to) set during reset
// and read back by whatever comes after it in the same decode call.
type tagTreeLevel struct {
	width, height int
	index         int
}

// tagTreeZeroBitPlanes decodes each code-block's zero-bit-plane count -
// see this file's doc comment. A code-block is only ever reset here
// once (on its first inclusion), so "visited" is enough state per node;
// there is no threshold to re-test on a later call the way
// tagTreeInclusion needs.
type tagTreeZeroBitPlanes struct {
	levels  []tagTreeLevel
	value   [][]int // value[level][index]
	visited [][]bool

	currentLevel int
	final        int
}

// newTagTreeZeroBitPlanes builds a tree over a width x height grid of
// code-blocks (one precinct's worth, within one subband).
func newTagTreeZeroBitPlanes(width, height int) *tagTreeZeroBitPlanes {
	t := &tagTreeZeroBitPlanes{}
	for {
		n := width * height
		t.levels = append(t.levels, tagTreeLevel{width: width, height: height})
		t.value = append(t.value, make([]int, n))
		t.visited = append(t.visited, make([]bool, n))
		if width <= 1 && height <= 1 {
			break
		}
		width = (width + 1) / 2
		height = (height + 1) / 2
	}
	return t
}

// reset begins decoding leaf (i,j) - a code-block's column/row within
// its precinct's code-block grid. It walks from the leaf toward the
// root looking for the coarsest ancestor some earlier leaf's decode
// already visited (and so already has a value recorded for); decoding
// only needs to proceed from there downward, since everything coarser
// is shared, already-decoded state.
func (t *tagTreeZeroBitPlanes) reset(i, j int) {
	currentLevel := 0
	value := 0
	for currentLevel < len(t.levels) {
		lvl := &t.levels[currentLevel]
		index := i + j*lvl.width
		if t.visited[currentLevel][index] {
			value = t.value[currentLevel][index]
			break
		}
		lvl.index = index
		i >>= 1
		j >>= 1
		currentLevel++
	}
	currentLevel--
	lvl := &t.levels[currentLevel]
	t.value[currentLevel][lvl.index] = value
	t.visited[currentLevel][lvl.index] = true
	t.currentLevel = currentLevel
}

// incrementValue records that the current level's node value is not yet
// final (a 0 bit was read against it): bump it by one and keep asking.
func (t *tagTreeZeroBitPlanes) incrementValue() {
	lvl := &t.levels[t.currentLevel]
	t.value[t.currentLevel][lvl.index]++
}

// nextLevel records that the current level's node value is final (a 1
// bit was read against it) and, unless that was level 0 itself (the
// leaf), descends to the next-finer level, seeding it with the same
// value as its own starting point (per the "child's value is at least
// its parent's" tag-tree invariant). Returns false once level 0 has
// just been finalized, at which point value() holds the result.
func (t *tagTreeZeroBitPlanes) nextLevel() bool {
	lvl := &t.levels[t.currentLevel]
	v := t.value[t.currentLevel][lvl.index]
	t.currentLevel--
	if t.currentLevel < 0 {
		t.final = v
		return false
	}
	lvl = &t.levels[t.currentLevel]
	t.value[t.currentLevel][lvl.index] = v
	t.visited[t.currentLevel][lvl.index] = true
	return true
}

// result returns the leaf's decoded zero-bit-plane count, valid only
// once nextLevel has returned false.
func (t *tagTreeZeroBitPlanes) result() int {
	return t.final
}

// tagTreeInclusion decodes, layer by layer, whether each code-block has
// become included yet - see this file's doc comment on why it needs a
// permanent "settled" flag the zero-bit-plane tree above does not.
type tagTreeInclusion struct {
	levels  []tagTreeLevel
	value   [][]int
	settled [][]bool

	currentLevel int
}

// newTagTreeInclusion builds an inclusion tree over a width x height
// code-block grid. defaultValue seeds every node's initial lower bound;
// it is ordinarily 0, except when a precinct's very first touched
// packet is not layer 0 (every earlier layer's packet for this precinct
// was empty), in which case it is that first layer's number - see
// packetheader.go's construction site for the full explanation, ported
// from pdf.js's InclusionTree constructor.
func newTagTreeInclusion(width, height, defaultValue int) *tagTreeInclusion {
	t := &tagTreeInclusion{}
	for {
		n := width * height
		t.levels = append(t.levels, tagTreeLevel{width: width, height: height})
		values := make([]int, n)
		for i := range values {
			values[i] = defaultValue
		}
		t.value = append(t.value, values)
		t.settled = append(t.settled, make([]bool, n))
		if width <= 1 && height <= 1 {
			break
		}
		width = (width + 1) / 2
		height = (height + 1) / 2
	}
	return t
}

// reset begins testing leaf (i,j) against stopValue (the layer number
// currently being parsed). It returns false, with nothing left to
// decode, in either of two cases where the answer is already known
// without reading a single bit: an ancestor is permanently settled
// (this code-block's subtree was already confirmed included by an
// earlier layer, and inclusion never turns back off), or an ancestor's
// running lower bound already exceeds stopValue (this code-block's
// subtree is already known not included by this layer). Both cases
// propagate the found node's value down to every finer level on this
// leaf's path first, so a later call (once stopValue reaches far enough)
// can resume decoding from the right place instead of re-deriving it.
func (t *tagTreeInclusion) reset(i, j, stopValue int) bool {
	currentLevel := 0
	for currentLevel < len(t.levels) {
		lvl := &t.levels[currentLevel]
		index := i + j*lvl.width
		lvl.index = index
		if t.settled[currentLevel][index] {
			break
		}
		if t.value[currentLevel][index] > stopValue {
			t.currentLevel = currentLevel
			t.propagateValues()
			return false
		}
		i >>= 1
		j >>= 1
		currentLevel++
	}
	t.currentLevel = currentLevel - 1
	return true
}

// incrementValue records that this code-block's subtree is confirmed
// NOT included by stopValue (a 0 bit was read against the current
// level): its lower bound becomes stopValue+1, meaning "the first layer
// with any inclusion here, whatever it turns out to be, is later than
// this one" - propagated down to finer levels on this leaf's path so a
// sibling sharing a coarser ancestor can pick up from here too.
func (t *tagTreeInclusion) incrementValue(stopValue int) {
	lvl := &t.levels[t.currentLevel]
	t.value[t.currentLevel][lvl.index] = stopValue + 1
	t.propagateValues()
}

func (t *tagTreeInclusion) propagateValues() {
	lvl := &t.levels[t.currentLevel]
	v := t.value[t.currentLevel][lvl.index]
	for l := t.currentLevel - 1; l >= 0; l-- {
		t.value[l][t.levels[l].index] = v
	}
}

// nextLevel records that this level's subtree is confirmed included by
// stopValue (a 1 bit was read against it): marks it permanently settled
// (per this type's doc comment, never decoded again regardless of which
// leaf asks) and, unless that was level 0 itself, descends to the next
// level with the same value as its starting point. Returns false once
// level 0 has just been settled - the code-block is included, for the
// first time, in the layer currently being parsed.
func (t *tagTreeInclusion) nextLevel() bool {
	lvl := &t.levels[t.currentLevel]
	v := t.value[t.currentLevel][lvl.index]
	t.settled[t.currentLevel][lvl.index] = true
	t.currentLevel--
	if t.currentLevel < 0 {
		return false
	}
	lvl = &t.levels[t.currentLevel]
	t.value[t.currentLevel][lvl.index] = v
	return true
}
