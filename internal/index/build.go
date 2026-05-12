package index

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
)

const (
	Magic   = "RIDX"
	Version = uint32(3)
	Dims    = 14
)

type VectorRecord struct {
	Vector [Dims]float32
	Fraud  bool
}

type rawRecord struct {
	Vector []float32 `json:"vector"`
	Label  string    `json:"label"` // "fraud" or "legit"
}


// LoadAllVectors reads all vectors from a gzip-compressed JSON array file.
func LoadAllVectors(path string) ([]VectorRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	var records []VectorRecord
	dec := json.NewDecoder(gr)
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	for dec.More() {
		var r rawRecord
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("decode error: %w", err)
		}
		var rec VectorRecord
		rec.Fraud = r.Label == "fraud"
		for i := range Dims {
			if i < len(r.Vector) {
				rec.Vector[i] = r.Vector[i]
			}
		}
		records = append(records, rec)
	}
	return records, nil
}

// BuildIndex runs k-means and writes the binary IVF index to outPath.
func BuildIndex(records []VectorRecord, outPath string, numClusters, iters int) error {
	fmt.Printf("Running k-means: %d vectors, %d clusters, %d iters\n", len(records), numClusters, iters)
	centroids := kMeans(records, numClusters, iters, 50000)

	fmt.Println("Assigning vectors to clusters...")
	clusterRecords := make([][]VectorRecord, numClusters)
	for i := range numClusters {
		clusterRecords[i] = make([]VectorRecord, 0, len(records)/numClusters+1)
	}
	for _, r := range records {
		c := nearestCentroid(r.Vector, centroids)
		clusterRecords[c] = append(clusterRecords[c], r)
	}

	// Pack clusters into dimension-major blocks of VecsPerBlock
	clusters := make([]Cluster, numClusters)
	for c, recs := range clusterRecords {
		numBlocks := (len(recs) + VecsPerBlock - 1) / VecsPerBlock
		blocks := make([]Block, numBlocks)
		for bi := range numBlocks {
			start := bi * VecsPerBlock
			end := start + VecsPerBlock
			if end > len(recs) {
				end = len(recs)
			}
			cnt := end - start
			blocks[bi].Count = cnt
			for vi := range cnt {
				q := QuantizeVec(recs[start+vi].Vector)
				for d := range Dims {
					blocks[bi].Dims[d][vi] = q[d]
				}
				if recs[start+vi].Fraud {
					blocks[bi].Labels[vi] = 1
				}
			}
		}
		clusters[c] = Cluster{Blocks: blocks, Count: len(recs)}
	}

	fmt.Printf("Writing index to %s...\n", outPath)
	return writeIndex(outPath, centroids, clusters, len(records))
}

func writeIndex(outPath string, centroids [][Dims]float32, clusters []Cluster, totalVecs int) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	w := newBinWriter(out)
	w.writeBytes([]byte(Magic))
	w.writeUint32(Version)
	w.writeUint32(uint32(len(centroids)))
	w.writeUint32(uint32(totalVecs))
	w.writeUint32(uint32(Dims))
	w.writeUint32(uint32(scale))

	for _, c := range centroids {
		for _, v := range c {
			w.writeFloat32(v)
		}
	}

	for _, cl := range clusters {
		w.writeUint32(uint32(cl.Count))
		w.writeUint32(uint32(len(cl.Blocks)))
		for _, b := range cl.Blocks {
			w.writeUint32(uint32(b.Count))
			// dimension-major: all VecsPerBlock values for each dim
			for d := range Dims {
				for v := range VecsPerBlock {
					w.writeInt16(b.Dims[d][v])
				}
			}
			// labels
			for v := range VecsPerBlock {
				w.writeByte(b.Labels[v])
			}
		}
	}
	return w.err
}

// --- k-means ---

func kMeans(records []VectorRecord, k, iters, batchSize int) [][Dims]float32 {
	perm := rand.Perm(len(records))
	centroids := make([][Dims]float32, k)
	for i := range k {
		centroids[i] = records[perm[i]].Vector
	}

	n := len(records)
	counts := make([]int64, k)
	sums := make([][Dims]float64, k)

	for iter := range iters {
		start := rand.Intn(n)
		end := start + batchSize
		if end > n {
			end = n
		}
		for c := range k {
			counts[c] = 0
			sums[c] = [Dims]float64{}
		}
		for _, r := range records[start:end] {
			c := nearestCentroid(r.Vector, centroids)
			counts[c]++
			for d := range Dims {
				sums[c][d] += float64(r.Vector[d])
			}
		}
		for c := range k {
			if counts[c] == 0 {
				continue
			}
			for d := range Dims {
				centroids[c][d] = float32(sums[c][d] / float64(counts[c]))
			}
		}
		if iter%2 == 0 {
			fmt.Printf("  k-means iter %d/%d\n", iter+1, iters)
		}
	}
	return centroids
}

func nearestCentroid(v [Dims]float32, centroids [][Dims]float32) int {
	best, bestDist := 0, math.MaxFloat64
	for i, c := range centroids {
		d := euclideanSq(v, c)
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

func euclideanSq(a, b [Dims]float32) float64 {
	var sum float64
	for i := range Dims {
		diff := float64(a[i] - b[i])
		sum += diff * diff
	}
	return sum
}

// --- binary writer ---

type binWriter struct {
	w   io.Writer
	buf [8]byte
	err error
}

func newBinWriter(w io.Writer) *binWriter { return &binWriter{w: w} }

func (bw *binWriter) writeBytes(b []byte) {
	if bw.err != nil {
		return
	}
	_, bw.err = bw.w.Write(b)
}

func (bw *binWriter) writeByte(b byte) {
	bw.buf[0] = b
	bw.writeBytes(bw.buf[:1])
}

func (bw *binWriter) writeUint32(v uint32) {
	binary.LittleEndian.PutUint32(bw.buf[:4], v)
	bw.writeBytes(bw.buf[:4])
}

func (bw *binWriter) writeInt16(v int16) {
	binary.LittleEndian.PutUint16(bw.buf[:2], uint16(v))
	bw.writeBytes(bw.buf[:2])
}

func (bw *binWriter) writeFloat32(v float32) {
	binary.LittleEndian.PutUint32(bw.buf[:4], math.Float32bits(v))
	bw.writeBytes(bw.buf[:4])
}
