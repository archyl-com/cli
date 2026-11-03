package diagram

import (
	"math"
	"slices"
	"sort"
)

// Point is a position in the layout's units: terminal cells for text,
// pixels for images.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Spacing sets the gaps of a layout, in its units.
type Spacing struct {
	// Margin surrounds the drawing.
	Margin int
	// NodeGap separates neighbouring boxes of a layer.
	NodeGap int
	// LaneWidth is the room an arrow takes when it crosses a layer.
	LaneWidth int
	// PadTop and PadBottom are the room above the first horizontal track of
	// a channel and below the last; TrackGap separates tracks.
	PadTop, TrackGap, PadBottom int
	// MinChannel is the least height between two layers.
	MinChannel int
	// SegmentGap is the least room between two segments sharing a track.
	SegmentGap int
}

// Box is a node placed on the canvas.
type Box struct {
	Node
	X     int `json:"x"`
	Y     int `json:"y"`
	W     int `json:"w"`
	H     int `json:"h"`
	Layer int `json:"layer"`
}

// Route is an edge drawn as an orthogonal polyline. Its first point is the
// cell next to the source box, its last the cell next to the target, where
// the arrowhead goes.
type Route struct {
	Edge
	Points []Point `json:"points"`
}

// Layout is a view placed on a canvas.
type Layout struct {
	View   *View   `json:"-"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Boxes  []Box   `json:"nodes"`
	Routes []Route `json:"edges"`
}

// Box returns the box of a node, or nil.
func (l *Layout) Box(id string) *Box {
	for i := range l.Boxes {
		if l.Boxes[i].ID == id {
			return &l.Boxes[i]
		}
	}
	return nil
}

// vnode is a box or, for an edge spanning several layers, the lane it takes
// through one of them.
type vnode struct {
	real   int // index in the view, -1 for a lane
	layer  int
	w, h   int
	up     []int
	down   []int
	center float64
}

// link is an edge laid top to bottom: its chain of vnodes from upper to lower.
type link struct {
	edge     int
	reversed bool
	chain    []int
}

// Arrange lays a view out in layers, top to bottom, the Sugiyama way: break
// cycles, assign layers by longest path, order each layer to reduce
// crossings, place boxes close to their neighbours, then route every edge
// orthogonally through the channels between layers. It is deterministic.
func Arrange(v *View, size func(Node) (w, h int), sp Spacing) *Layout {
	n := len(v.Nodes)
	if n == 0 {
		return &Layout{View: v, Width: 2 * sp.Margin, Height: 2 * sp.Margin}
	}
	index := make(map[string]int, n)
	for i, node := range v.Nodes {
		index[node.ID] = i
	}
	type arc struct{ from, to, edge int }
	var arcs []arc
	for i, e := range v.Edges {
		a, okA := index[e.From]
		b, okB := index[e.To]
		if okA && okB && a != b {
			arcs = append(arcs, arc{a, b, i})
		}
	}

	// Break cycles: an arc into a node still on the DFS stack is laid out
	// reversed.
	out := make([][]int, n) // arc indexes
	for i, a := range arcs {
		out[a.from] = append(out[a.from], i)
	}
	reversed := make([]bool, len(arcs))
	state := make([]int, n) // 0 new, 1 on stack, 2 done
	var visit func(int)
	visit = func(u int) {
		state[u] = 1
		for _, ai := range out[u] {
			w := arcs[ai].to
			switch state[w] {
			case 0:
				visit(w)
			case 1:
				reversed[ai] = true
			}
		}
		state[u] = 2
	}
	for u := range n {
		if state[u] == 0 {
			visit(u)
		}
	}
	succ := make([][]int, n)
	pred := make([][]int, n)
	connected := make([]bool, n)
	for i, a := range arcs {
		u, w := a.from, a.to
		if reversed[i] {
			u, w = w, u
		}
		succ[u] = append(succ[u], w)
		pred[w] = append(pred[w], u)
		connected[u], connected[w] = true, true
	}

	// Longest path layering, then sources pulled down next to what they use.
	layer := make([]int, n)
	order := topoOrder(n, succ, pred)
	for _, u := range order {
		for _, w := range succ[u] {
			layer[w] = max(layer[w], layer[u]+1)
		}
	}
	for i := len(order) - 1; i >= 0; i-- {
		u := order[i]
		if len(pred[u]) == 0 && len(succ[u]) > 0 {
			lowest := math.MaxInt
			for _, w := range succ[u] {
				lowest = min(lowest, layer[w])
			}
			layer[u] = lowest - 1
		}
	}
	layers := 0
	widest := 0
	perLayer := map[int]int{}
	for u := range n {
		if connected[u] {
			layers = max(layers, layer[u]+1)
			perLayer[layer[u]]++
			widest = max(widest, perLayer[layer[u]])
		}
	}
	// Loose nodes fill rows of their own under the rest.
	var loose []int
	for u := range n {
		if !connected[u] {
			loose = append(loose, u)
		}
	}
	if len(loose) > 0 {
		perRow := max(widest, int(math.Ceil(math.Sqrt(float64(len(loose))*2))), 3)
		for i, u := range loose {
			layer[u] = layers + i/perRow
		}
		layers += (len(loose) + perRow - 1) / perRow
	}

	// Virtual graph: boxes, plus one lane per layer an edge crosses.
	vs := make([]vnode, 0, n)
	for u, node := range v.Nodes {
		w, h := size(node)
		vs = append(vs, vnode{real: u, layer: layer[u], w: w, h: h})
	}
	links := make([]link, 0, len(arcs))
	for i, a := range arcs {
		u, w := a.from, a.to
		if reversed[i] {
			u, w = w, u
		}
		chain := []int{u}
		for l := layer[u] + 1; l < layer[w]; l++ {
			vs = append(vs, vnode{real: -1, layer: l, w: sp.LaneWidth, h: 0})
			chain = append(chain, len(vs)-1)
		}
		chain = append(chain, w)
		for k := 0; k+1 < len(chain); k++ {
			vs[chain[k]].down = append(vs[chain[k]].down, chain[k+1])
			vs[chain[k+1]].up = append(vs[chain[k+1]].up, chain[k])
		}
		links = append(links, link{edge: a.edge, reversed: reversed[i], chain: chain})
	}

	rows := make([][]int, layers)
	for i, vn := range vs {
		rows[vn.layer] = append(rows[vn.layer], i)
	}
	orderLayers(vs, rows)
	placeX(vs, rows, sp)

	// Vertical placement: layer bands separated by channels sized by their
	// tracks.
	ports := assignPorts(vs, links)
	tracks := make([]map[[2]int]int, layers) // per channel: (link, hop) -> track
	trackCount := make([]int, layers)
	for l := 0; l+1 < layers; l++ {
		tracks[l], trackCount[l] = assignTracks(vs, links, ports, l, sp.SegmentGap)
	}
	hopsBelow := make([]bool, layers)
	for _, vn := range vs {
		if len(vn.down) > 0 {
			hopsBelow[vn.layer] = true
		}
	}
	top := make([]int, layers)
	bandHeight := make([]int, layers)
	for _, vn := range vs {
		bandHeight[vn.layer] = max(bandHeight[vn.layer], vn.h)
	}
	y := sp.Margin
	channelTop := make([]int, layers)
	for l := range layers {
		top[l] = y
		y += max(bandHeight[l], 1)
		if l+1 < layers {
			channelTop[l] = y
			if hopsBelow[l] {
				y += max(sp.PadTop+trackCount[l]*sp.TrackGap+sp.PadBottom, sp.MinChannel)
			} else {
				y += sp.PadTop + sp.PadBottom/2
			}
		}
	}
	height := y + sp.Margin

	lay := &Layout{View: v, Height: height}
	width := 0
	for i := range n {
		vn := vs[i]
		left := int(math.Round(vn.center - float64(vn.w)/2))
		lay.Boxes = append(lay.Boxes, Box{Node: v.Nodes[i], X: left, Y: top[vn.layer], W: vn.w, H: vn.h, Layer: vn.layer})
		width = max(width, left+vn.w)
	}
	for _, vn := range vs {
		width = max(width, int(math.Round(vn.center))+1)
	}
	lay.Width = width + sp.Margin

	for _, lk := range links {
		var pts []Point
		first := lk.chain[0]
		pts = append(pts, Point{ports[[3]int{lk.edge, 0, 0}], top[vs[first].layer] + vs[first].h})
		for k := 0; k+1 < len(lk.chain); k++ {
			a, b := lk.chain[k], lk.chain[k+1]
			x1, x2 := ports[[3]int{lk.edge, k, 0}], ports[[3]int{lk.edge, k, 1}]
			if vs[a].real < 0 {
				// A lane runs through its whole band.
				pts = append(pts, Point{x1, top[vs[a].layer] + max(bandHeight[vs[a].layer], 1)})
			}
			if x1 != x2 {
				ty := channelTop[vs[a].layer] + sp.PadTop + tracks[vs[a].layer][[2]int{lk.edge, k}]*sp.TrackGap
				pts = append(pts, Point{x1, ty}, Point{x2, ty})
			}
			if vs[b].real < 0 {
				pts = append(pts, Point{x2, top[vs[b].layer]})
			} else {
				pts = append(pts, Point{x2, top[vs[b].layer] - 1})
			}
		}
		pts = simplify(pts)
		if lk.reversed {
			slices.Reverse(pts)
		}
		lay.Routes = append(lay.Routes, Route{Edge: v.Edges[lk.edge], Points: pts})
	}
	return lay
}

// topoOrder sorts the DAG, sources in view order first.
func topoOrder(n int, succ, pred [][]int) []int {
	indeg := make([]int, n)
	for u := range n {
		indeg[u] = len(pred[u])
	}
	var queue, order []int
	for u := range n {
		if indeg[u] == 0 {
			queue = append(queue, u)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, w := range succ[u] {
			indeg[w]--
			if indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	return order
}

// orderLayers reduces crossings: barycentre sweeps down and up, keeping the
// best order seen, then swaps of neighbours while they help.
func orderLayers(vs []vnode, rows [][]int) {
	pos := make([]float64, len(vs))
	setPos := func() {
		for _, row := range rows {
			for i, u := range row {
				pos[u] = float64(i)
			}
		}
	}
	setPos()
	snapshot := func() [][]int {
		out := make([][]int, len(rows))
		for i, row := range rows {
			out[i] = slices.Clone(row)
		}
		return out
	}
	best, bestCross := snapshot(), crossings(vs, rows, pos)
	sortBy := func(row []int, neighbours func(int) []int) {
		bary := make(map[int]float64, len(row))
		for _, u := range row {
			nb := neighbours(u)
			if len(nb) == 0 {
				bary[u] = pos[u]
				continue
			}
			sum := 0.0
			for _, w := range nb {
				sum += pos[w]
			}
			bary[u] = sum / float64(len(nb))
		}
		sort.SliceStable(row, func(i, j int) bool { return bary[row[i]] < bary[row[j]] })
		for i, u := range row {
			pos[u] = float64(i)
		}
	}
	for iter := range 24 {
		if iter%2 == 0 {
			for l := 1; l < len(rows); l++ {
				sortBy(rows[l], func(u int) []int { return vs[u].up })
			}
		} else {
			for l := len(rows) - 2; l >= 0; l-- {
				sortBy(rows[l], func(u int) []int { return vs[u].down })
			}
		}
		if c := crossings(vs, rows, pos); c < bestCross {
			best, bestCross = snapshot(), c
		}
	}
	copy(rows, best)
	setPos()

	for range 8 {
		improved := false
		for _, row := range rows {
			for i := 0; i+1 < len(row); i++ {
				before := crossings(vs, rows, pos)
				row[i], row[i+1] = row[i+1], row[i]
				pos[row[i]], pos[row[i+1]] = float64(i), float64(i+1)
				if crossings(vs, rows, pos) < before {
					improved = true
					continue
				}
				row[i], row[i+1] = row[i+1], row[i]
				pos[row[i]], pos[row[i+1]] = float64(i), float64(i+1)
			}
		}
		if !improved {
			break
		}
	}
}

// crossings counts the pairs of hops that cross between adjacent layers.
func crossings(vs []vnode, rows [][]int, pos []float64) int {
	total := 0
	for _, row := range rows {
		var hops [][2]float64
		for _, u := range row {
			for _, w := range vs[u].down {
				hops = append(hops, [2]float64{pos[u], pos[w]})
			}
		}
		for i := range hops {
			for j := i + 1; j < len(hops); j++ {
				a, b := hops[i], hops[j]
				if (a[0]-b[0])*(a[1]-b[1]) < 0 {
					total++
				}
			}
		}
	}
	return total
}

// placeX sets each vnode's centre: packed rows first, then sweeps that pull
// every node toward the mean of its neighbours, solved per row as a weighted
// isotonic regression so boxes never overlap.
func placeX(vs []vnode, rows [][]int, sp Spacing) {
	for _, row := range rows {
		x := float64(sp.Margin)
		for _, u := range row {
			vs[u].center = x + float64(vs[u].w)/2
			x += float64(vs[u].w + sp.NodeGap)
		}
	}
	mean := func(nb []int) (float64, bool) {
		if len(nb) == 0 {
			return 0, false
		}
		sum := 0.0
		for _, w := range nb {
			sum += vs[w].center
		}
		return sum / float64(len(nb)), true
	}
	for iter := range 16 {
		apply := func(row []int) {
			desired := make([]float64, len(row))
			weights := make([]float64, len(row))
			for i, u := range row {
				var nb []int
				switch {
				case iter >= 12:
					nb = append(append(nb, vs[u].up...), vs[u].down...)
				case iter%2 == 0:
					nb = vs[u].up
				default:
					nb = vs[u].down
				}
				d, ok := mean(nb)
				if !ok {
					d = vs[u].center
				}
				desired[i] = d
				weights[i] = 1
				if vs[u].real < 0 {
					weights[i] = 2
				}
			}
			centers := solveRow(row, vs, desired, weights, float64(sp.NodeGap))
			for i, u := range row {
				vs[u].center = centers[i]
			}
		}
		if iter%2 == 0 {
			for _, row := range rows {
				apply(row)
			}
		} else {
			for l := len(rows) - 1; l >= 0; l-- {
				apply(rows[l])
			}
		}
	}

	// Shift onto the canvas and snap to whole units without overlaps.
	minLeft := math.Inf(1)
	for _, vn := range vs {
		minLeft = min(minLeft, vn.center-float64(vn.w)/2)
	}
	shift := float64(sp.Margin) - minLeft
	for _, row := range rows {
		prevRight := math.Inf(-1)
		for _, u := range row {
			left := math.Round(vs[u].center - float64(vs[u].w)/2 + shift)
			if left < prevRight+float64(sp.NodeGap) {
				left = prevRight + float64(sp.NodeGap)
			}
			vs[u].center = left + float64(vs[u].w)/2
			if vs[u].real < 0 {
				vs[u].center = left
			}
			prevRight = left + float64(vs[u].w)
		}
	}
}

// solveRow places a row's boxes in order, left edges at least a gap apart,
// minimising the weighted squared distance to the desired centres
// (pool-adjacent-violators on the offsets).
func solveRow(row []int, vs []vnode, desired, weights []float64, gap float64) []float64 {
	type block struct {
		sum, weight float64
		count       int
	}
	offsets := make([]float64, len(row))
	acc := 0.0
	for i, u := range row {
		offsets[i] = acc
		acc += float64(vs[u].w) + gap
	}
	var blocks []block
	for i, u := range row {
		target := desired[i] - float64(vs[u].w)/2 - offsets[i]
		blocks = append(blocks, block{target * weights[i], weights[i], 1})
		for len(blocks) >= 2 {
			a, b := blocks[len(blocks)-2], blocks[len(blocks)-1]
			if a.sum/a.weight <= b.sum/b.weight {
				break
			}
			blocks = blocks[:len(blocks)-2]
			blocks = append(blocks, block{a.sum + b.sum, a.weight + b.weight, a.count + b.count})
		}
	}
	centers := make([]float64, 0, len(row))
	i := 0
	for _, b := range blocks {
		value := b.sum / b.weight
		for range b.count {
			centers = append(centers, value+offsets[i]+float64(vs[row[i]].w)/2)
			i++
		}
	}
	return centers
}

// assignPorts picks the x where each hop leaves its upper vnode and enters
// its lower one, keyed by (edge, hop, 0 for the top end or 1 for the bottom).
// Ports spread along a box's side in the order of the other ends, then snap
// straight when the other end lies within reach.
func assignPorts(vs []vnode, links []link) map[[3]int]int {
	ports := map[[3]int]int{}
	type end struct {
		key   [3]int
		other int
	}
	outs := map[int][]end{}
	ins := map[int][]end{}
	for _, lk := range links {
		for k := 0; k+1 < len(lk.chain); k++ {
			a, b := lk.chain[k], lk.chain[k+1]
			outs[a] = append(outs[a], end{[3]int{lk.edge, k, 0}, b})
			ins[b] = append(ins[b], end{[3]int{lk.edge, k, 1}, a})
		}
	}
	spread := func(u int, ends []end) {
		vn := vs[u]
		if vn.real < 0 {
			for _, e := range ends {
				ports[e.key] = int(math.Round(vn.center))
			}
			return
		}
		sort.SliceStable(ends, func(i, j int) bool { return vs[ends[i].other].center < vs[ends[j].other].center })
		left := vn.center - float64(vn.w)/2
		for i, e := range ends {
			ports[e.key] = int(math.Floor(left + float64(vn.w)*float64(i+1)/float64(len(ends)+1)))
		}
	}
	for u, ends := range outs {
		spread(u, ends)
	}
	for u, ends := range ins {
		spread(u, ends)
	}
	// Straighten a hop when one end has it alone and the other end's port
	// falls inside its box.
	inside := func(u, x int) bool {
		vn := vs[u]
		left := int(math.Round(vn.center - float64(vn.w)/2))
		return vn.real >= 0 && x > left+1 && x < left+vn.w-2
	}
	for _, lk := range links {
		for k := 0; k+1 < len(lk.chain); k++ {
			a, b := lk.chain[k], lk.chain[k+1]
			top, bottom := [3]int{lk.edge, k, 0}, [3]int{lk.edge, k, 1}
			switch {
			case ports[top] == ports[bottom]:
			case len(outs[a]) == 1 && inside(a, ports[bottom]):
				ports[top] = ports[bottom]
			case len(ins[b]) == 1 && inside(b, ports[top]):
				ports[bottom] = ports[top]
			}
		}
	}
	return ports
}

// assignTracks gives each bent hop of channel l a horizontal track. Hops
// whose spans do not overlap share a track; overlapping ones are stacked in
// the order that crosses the fewest vertical runs.
func assignTracks(vs []vnode, links []link, ports map[[3]int]int, l, gap int) (map[[2]int]int, int) {
	type hop struct {
		key            [2]int
		x1, x2, lo, hi int
	}
	var hops []hop
	for _, lk := range links {
		for k := 0; k+1 < len(lk.chain); k++ {
			if vs[lk.chain[k]].layer != l {
				continue
			}
			x1, x2 := ports[[3]int{lk.edge, k, 0}], ports[[3]int{lk.edge, k, 1}]
			if x1 != x2 {
				hops = append(hops, hop{[2]int{lk.edge, k}, x1, x2, min(x1, x2), max(x1, x2)})
			}
		}
	}
	within := func(x int, h hop) bool { return x > h.lo && x < h.hi }
	overlap := func(a, b hop) bool { return a.lo <= b.hi+gap && b.lo <= a.hi+gap }
	// cost counts the crossings when a runs above b: a's way down crosses b's
	// track, b's way down from the upper layer crosses a's.
	cost := func(a, b hop) int {
		c := 0
		if within(a.x2, b) {
			c++
		}
		if within(b.x1, a) {
			c++
		}
		return c
	}
	above := func(a, b hop) bool { return cost(a, b) < cost(b, a) }
	score := make([]int, len(hops))
	for i := range hops {
		for j := range hops {
			if i != j && overlap(hops[i], hops[j]) && above(hops[i], hops[j]) {
				score[i]++
			}
		}
	}
	idx := make([]int, len(hops))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if score[idx[a]] != score[idx[b]] {
			return score[idx[a]] > score[idx[b]]
		}
		return hops[idx[a]].lo < hops[idx[b]].lo
	})

	assigned := map[[2]int]int{}
	var placed []int
	var onTrack [][]hop
	for _, i := range idx {
		h := hops[i]
		floor := 0
		for _, j := range placed {
			if overlap(h, hops[j]) && above(hops[j], h) {
				floor = max(floor, assigned[hops[j].key]+1)
			}
		}
		t := floor
		for ; t < len(onTrack); t++ {
			clear := true
			for _, o := range onTrack[t] {
				if overlap(h, o) {
					clear = false
					break
				}
			}
			if clear {
				break
			}
		}
		for t >= len(onTrack) {
			onTrack = append(onTrack, nil)
		}
		onTrack[t] = append(onTrack[t], h)
		assigned[h.key] = t
		placed = append(placed, i)
	}
	return assigned, len(onTrack)
}

// simplify drops repeated points and the middle of straight runs.
func simplify(pts []Point) []Point {
	out := make([]Point, 0, len(pts))
	for _, p := range pts {
		if len(out) > 0 && out[len(out)-1] == p {
			continue
		}
		if len(out) >= 2 {
			a, b := out[len(out)-2], out[len(out)-1]
			if (a.X == b.X && b.X == p.X) || (a.Y == b.Y && b.Y == p.Y) {
				out[len(out)-1] = p
				continue
			}
		}
		out = append(out, p)
	}
	return out
}
