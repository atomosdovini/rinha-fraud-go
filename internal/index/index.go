package index

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Block stores VecsPerBlock vectors in dimension-major layout for SIMD-friendly access.
type Block struct {
	Dims   [Dims][VecsPerBlock]int16
	Labels [VecsPerBlock]uint8
	Count  int
}

type Cluster struct {
	Blocks []Block
	Count  int
}

type Index struct {
	Centroids [][Dims]float32
	Clusters  []Cluster
	ready     atomic.Bool
}

func Load(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	off := 0
	readBytes := func(n int) []byte { b := data[off : off+n]; off += n; return b }
	readUint32 := func() uint32 { v := binary.LittleEndian.Uint32(data[off:]); off += 4; return v }
	readInt16 := func() int16 {
		v := int16(binary.LittleEndian.Uint16(data[off:]))
		off += 2
		return v
	}
	readFloat32 := func() float32 {
		bits := binary.LittleEndian.Uint32(data[off:])
		off += 4
		return *(*float32)(unsafe.Pointer(&bits))
	}

	magic := string(readBytes(4))
	if magic != Magic {
		return nil, fmt.Errorf("invalid magic: %q", magic)
	}
	version := readUint32()
	if version != Version {
		return nil, fmt.Errorf("unsupported index version %d (want %d)", version, Version)
	}
	numClusters := int(readUint32())
	_ = readUint32() // total_vectors
	dims := int(readUint32())
	_ = readUint32() // scale

	if dims != Dims {
		return nil, fmt.Errorf("unexpected dims %d", dims)
	}

	centroids := make([][Dims]float32, numClusters)
	for c := range numClusters {
		for d := range Dims {
			centroids[c][d] = readFloat32()
		}
	}

	clusters := make([]Cluster, numClusters)
	for c := range numClusters {
		count := int(readUint32())
		numBlocks := int(readUint32())
		blocks := make([]Block, numBlocks)
		for bi := range numBlocks {
			blocks[bi].Count = int(readUint32())
			for d := range Dims {
				for v := range VecsPerBlock {
					blocks[bi].Dims[d][v] = readInt16()
				}
			}
			for v := range VecsPerBlock {
				blocks[bi].Labels[v] = data[off]
				off++
			}
		}
		clusters[c] = Cluster{Blocks: blocks, Count: count}
	}

	idx := &Index{Centroids: centroids, Clusters: clusters}
	idx.ready.Store(true)
	return idx, nil
}

func (idx *Index) IsReady() bool { return idx.ready.Load() }

type centEntry struct {
	idx  int
	dist float32
}

var centPool = sync.Pool{New: func() any {
	s := make([]centEntry, 0, 8192)
	return &s
}}

type candidate struct {
	distSq int64
	label  uint8
}

type heapBuf struct {
	h [6]candidate
	n int
}

var heapPool = sync.Pool{New: func() any { return &heapBuf{} }}

// Search finds the k nearest neighbors using IVF with nprobe clusters.
// Returns the count of fraud labels among the k nearest.
func (idx *Index) Search(query [Dims]float32, k, nprobe int) int {
	numCents := len(idx.Centroids)

	// 1. Compute float32 distances to all centroids
	sp := centPool.Get().(*[]centEntry)
	cents := *sp
	if cap(cents) < numCents {
		cents = make([]centEntry, numCents)
	}
	cents = cents[:numCents]

	for i := range numCents {
		c := &idx.Centroids[i]
		d0 := query[0] - c[0]
		d1 := query[1] - c[1]
		d2 := query[2] - c[2]
		d3 := query[3] - c[3]
		d4 := query[4] - c[4]
		d5 := query[5] - c[5]
		d6 := query[6] - c[6]
		d7 := query[7] - c[7]
		d8 := query[8] - c[8]
		d9 := query[9] - c[9]
		d10 := query[10] - c[10]
		d11 := query[11] - c[11]
		d12 := query[12] - c[12]
		d13 := query[13] - c[13]
		cents[i] = centEntry{i, d0*d0 + d1*d1 + d2*d2 + d3*d3 + d4*d4 + d5*d5 + d6*d6 +
			d7*d7 + d8*d8 + d9*d9 + d10*d10 + d11*d11 + d12*d12 + d13*d13}
	}

	// Partial selection sort — top nprobe; copy indices before releasing pool
	var probeIdx [8]int // nprobe ≤ 8
	for i := range nprobe {
		minJ := i
		for j := i + 1; j < numCents; j++ {
			if cents[j].dist < cents[minJ].dist {
				minJ = j
			}
		}
		cents[i], cents[minJ] = cents[minJ], cents[i]
		probeIdx[i] = cents[i].idx
	}
	*sp = cents
	centPool.Put(sp)

	// 2. Quantize query to int32
	q := QueryInt32(query)

	// 3. Scan probed clusters with early pruning per block
	hb := heapPool.Get().(*heapBuf)
	hb.n = 0
	heapMax := int64(math.MaxInt64)

	for _, ci := range probeIdx[:nprobe] {
		cl := &idx.Clusters[ci]
		for bi := range cl.Blocks {
			b := &cl.Blocks[bi]
			cnt := b.Count

			// 4-dim partial in int32 — fits (max 4×20000²=1.6B < 2.1B), 8×int32 = 1 AVX2 op
			var p4 [VecsPerBlock]int32
			for v := range VecsPerBlock {
				a0 := q[0] - int32(b.Dims[0][v])
				a1 := q[1] - int32(b.Dims[1][v])
				a2 := q[2] - int32(b.Dims[2][v])
				a3 := q[3] - int32(b.Dims[3][v])
				p4[v] = a0*a0 + a1*a1 + a2*a2 + a3*a3
			}
			heapMax32 := int32(min(heapMax, math.MaxInt32))

			anyAlive := false
			for v := range VecsPerBlock {
				if p4[v] < heapMax32 {
					anyAlive = true
					break
				}
			}
			if !anyAlive {
				continue
			}

			// Extend to 8 dims (may exceed int32, use int64)
			var partial [VecsPerBlock]int64
			for v := range VecsPerBlock {
				a4 := q[4] - int32(b.Dims[4][v])
				a5 := q[5] - int32(b.Dims[5][v])
				a6 := q[6] - int32(b.Dims[6][v])
				a7 := q[7] - int32(b.Dims[7][v])
				partial[v] = int64(p4[v]) + int64(a4*a4+a5*a5+a6*a6+a7*a7)
			}

			anyAlive = false
			for v := range VecsPerBlock {
				if partial[v] < heapMax {
					anyAlive = true
					break
				}
			}
			if !anyAlive {
				continue
			}

			// Full distance for remaining dims
			for v := range cnt {
				if partial[v] >= heapMax {
					continue
				}
				a8 := q[8] - int32(b.Dims[8][v])
				a9 := q[9] - int32(b.Dims[9][v])
				a10 := q[10] - int32(b.Dims[10][v])
				a11 := q[11] - int32(b.Dims[11][v])
				a12 := q[12] - int32(b.Dims[12][v])
				a13 := q[13] - int32(b.Dims[13][v])
				distSq := partial[v] + int64(a8*a8+a9*a9+a10*a10+a11*a11+a12*a12+a13*a13)

				if hb.n < k || distSq < heapMax {
					lbl := b.Labels[v]
					if hb.n < k {
						hb.h[hb.n] = candidate{distSq, lbl}
						hb.n++
						heapifyUp(hb.h[:hb.n], hb.n-1)
					} else {
						hb.h[0] = candidate{distSq, lbl}
						heapifyDown(hb.h[:hb.n], 0)
					}
					heapMax = hb.h[0].distSq
				}
			}
		}
	}

	fraudCount := 0
	for i := range hb.n {
		if hb.h[i].label == 1 {
			fraudCount++
		}
	}

	heapPool.Put(hb)
	return fraudCount
}

func heapifyUp(h []candidate, i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h[i].distSq > h[parent].distSq {
			h[i], h[parent] = h[parent], h[i]
			i = parent
		} else {
			break
		}
	}
}

func heapifyDown(h []candidate, i int) {
	n := len(h)
	for {
		largest := i
		l, r := 2*i+1, 2*i+2
		if l < n && h[l].distSq > h[largest].distSq {
			largest = l
		}
		if r < n && h[r].distSq > h[largest].distSq {
			largest = r
		}
		if largest == i {
			break
		}
		h[i], h[largest] = h[largest], h[i]
		i = largest
	}
}
