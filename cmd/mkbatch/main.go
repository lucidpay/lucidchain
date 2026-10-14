package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"github.com/lucidpay/lucidchain/tools/zkgeo"
)

func salt() string {
	b := make([]byte, 31) // 31 bytes is always below the BN254 field modulus
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "0x" + hex.EncodeToString(b)
}

func main() {
	const shift = 14 // must equal the -cell-shift used at setup (default 14)
	ax, ay := zkgeo.EncodeLonLat(55.27, 25.20)
	step := uint32(1) << shift
	pts := [4][2]uint32{{ax, ay}, {ax + step, ay}, {ax, ay + step}, {ax + step, ay + step}}

	cells := make([]uint64, 0, 4)
	for _, p := range pts {
		cells = append(cells, zkgeo.CellID(p[0], p[1], shift))
	}

	start := uint64(time.Now().Unix()) - 3600
	type rec struct {
		X    uint32 `json:"x"`
		Y    uint32 `json:"y"`
		T    uint64 `json:"t"`
		Salt string `json:"salt"`
	}
	var recs []rec
	for i := 0; i < 5; i++ {
		p := pts[i%4]
		recs = append(recs, rec{X: p[0], Y: p[1], T: start + 10*uint64(i+1), Salt: salt()})
	}
	out := map[string]any{
		"window_start": start, "window_end": start + 7200,
		"area_salt": salt(), "area_cells": cells, "records": recs,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
