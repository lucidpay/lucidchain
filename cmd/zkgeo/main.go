// Command zkgeo generates keys and proofs for the batch geofence circuit, in the
// exact encodings x/proofs expects:
//
//	vk.bin / pk.bin : gnark WriteTo
//	proof.b64       : base64(proof.WriteTo)            -> --proof
//	public.b64      : base64(publicWitness.MarshalBinary) -> --public-inputs
//
// setup is DEV ONLY (single-party, insecure): production needs a real ceremony.
//
//	zkgeo setup -scheme groth16|plonk -out DIR
//	zkgeo prove -scheme groth16|plonk -pk DIR/pk.bin -vk DIR/vk.bin -hi H -lo L -batch batch.json -out DIR
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bn254/fr"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/backend/plonk"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/frontend/cs/scs"
	"github.com/consensys/gnark/test/unsafekzg"

	"github.com/lucidpay/lucidchain/tools/zkgeo"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: zkgeo setup|prove [flags]")
	}
	var err error
	switch os.Args[1] {
	case "setup":
		err = cmdSetup(os.Args[2:])
	case "prove":
		err = cmdProve(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }

type common struct {
	cfg    zkgeo.Config
	scheme string
}

func addCommon(fs *flag.FlagSet) *common {
	d := zkgeo.DefaultConfig()
	c := &common{}
	fs.StringVar(&c.scheme, "scheme", "groth16", "groth16 | plonk (BN254)")
	fs.IntVar(&c.cfg.CellShift, "cell-shift", d.CellShift, "grid cell shift (circuit parameter)")
	fs.IntVar(&c.cfg.Capacity, "capacity", d.Capacity, "records per proof, power of two (circuit parameter)")
	fs.IntVar(&c.cfg.RecordsDepth, "records-depth", d.RecordsDepth, "sidechain records tree depth (circuit parameter)")
	fs.IntVar(&c.cfg.AreaDepth, "area-depth", d.AreaDepth, "area tree depth (circuit parameter)")
	return c
}

func (c *common) compile() (constraint.ConstraintSystem, error) {
	if err := c.cfg.Validate(); err != nil {
		return nil, err
	}
	field := ecc.BN254.ScalarField()
	switch c.scheme {
	case "groth16":
		return frontend.Compile(field, r1cs.NewBuilder, zkgeo.NewCircuit(c.cfg))
	case "plonk":
		return frontend.Compile(field, scs.NewBuilder, zkgeo.NewCircuit(c.cfg))
	}
	return nil, fmt.Errorf("unknown scheme %q", c.scheme)
}

func writeTo(path string, w io.WriterTo) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = w.WriteTo(f)
	return err
}

func readFrom(path string, r io.ReaderFrom) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = r.ReadFrom(f)
	return err
}

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	c := addCommon(fs)
	out := fs.String("out", ".", "output directory")
	fs.Parse(args)

	ccs, err := c.compile()
	if err != nil {
		return err
	}
	fmt.Printf("constraints: %d, public inputs: %d\n", ccs.GetNbConstraints(), zkgeo.NumPublic)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}

	switch c.scheme {
	case "groth16":
		pk, vk, err := groth16.Setup(ccs)
		if err != nil {
			return err
		}
		if err := writeTo(filepath.Join(*out, "pk.bin"), pk); err != nil {
			return err
		}
		return writeTo(filepath.Join(*out, "vk.bin"), vk)
	default:
		srs, srsLagrange, err := unsafekzg.NewSRS(ccs) // INSECURE: dev only
		if err != nil {
			return err
		}
		pk, vk, err := plonk.Setup(ccs, srs, srsLagrange)
		if err != nil {
			return err
		}
		if err := writeTo(filepath.Join(*out, "pk.bin"), pk); err != nil {
			return err
		}
		return writeTo(filepath.Join(*out, "vk.bin"), vk)
	}
}

type batchFile struct {
	WindowStart uint64   `json:"window_start"`
	WindowEnd   uint64   `json:"window_end"`
	AreaSalt    string   `json:"area_salt"`
	AreaCells   []uint64 `json:"area_cells"`
	Records     []struct {
		X    uint32 `json:"x"`
		Y    uint32 `json:"y"`
		T    uint64 `json:"t"`
		Salt string `json:"salt"`
	} `json:"records"`
}

func parseFr(s string) (fr.Element, error) {
	var e fr.Element
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) > 32 {
		return e, fmt.Errorf("bad field element %q", s)
	}
	var buf [32]byte
	copy(buf[32-len(b):], b)
	if err := e.SetBytesCanonical(buf[:]); err != nil {
		return e, fmt.Errorf("non-canonical field element %q", s)
	}
	return e, nil
}

func cmdProve(args []string) error {
	fs := flag.NewFlagSet("prove", flag.ExitOnError)
	c := addCommon(fs)
	pkPath := fs.String("pk", "pk.bin", "proving key")
	vkPath := fs.String("vk", "vk.bin", "verifying key (used for a local self-check)")
	hi := fs.String("hi", "", "binding hi limb (from `q proofs binding`)")
	lo := fs.String("lo", "", "binding lo limb")
	batchPath := fs.String("batch", "batch.json", "batch description")
	out := fs.String("out", ".", "output directory")
	fs.Parse(args)

	bHi, ok1 := new(big.Int).SetString(*hi, 0)
	bLo, ok2 := new(big.Int).SetString(*lo, 0)
	if !ok1 || !ok2 {
		return fmt.Errorf("-hi/-lo must be integers (decimal or 0x hex)")
	}

	raw, err := os.ReadFile(*batchPath)
	if err != nil {
		return err
	}
	var bf batchFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		return err
	}
	salt, err := parseFr(bf.AreaSalt)
	if err != nil {
		return err
	}
	area, err := zkgeo.NewArea(bf.AreaCells, c.cfg.AreaDepth, salt)
	if err != nil {
		return err
	}
	in := zkgeo.BatchInputs{
		Area: area, WindowStart: bf.WindowStart, WindowEnd: bf.WindowEnd,
		BindingHi: bHi, BindingLo: bLo,
	}
	for i, r := range bf.Records {
		s, err := parseFr(r.Salt)
		if err != nil {
			return fmt.Errorf("record %d: %w", i, err)
		}
		in.Records = append(in.Records, zkgeo.Record{X: r.X, Y: r.Y, T: r.T, Salt: s})
	}
	assign, err := zkgeo.BuildAssignment(c.cfg, in)
	if err != nil {
		return err
	}

	ccs, err := c.compile()
	if err != nil {
		return err
	}
	full, err := frontend.NewWitness(assign, ecc.BN254.ScalarField())
	if err != nil {
		return err
	}
	pub, err := full.Public()
	if err != nil {
		return err
	}

	var proofBuf bytes.Buffer
	switch c.scheme {
	case "groth16":
		pk, vk := groth16.NewProvingKey(ecc.BN254), groth16.NewVerifyingKey(ecc.BN254)
		if err := readFrom(*pkPath, pk); err != nil {
			return err
		}
		if err := readFrom(*vkPath, vk); err != nil {
			return err
		}
		proof, err := groth16.Prove(ccs, pk, full)
		if err != nil {
			return err
		}
		if err := groth16.Verify(proof, vk, pub); err != nil {
			return fmt.Errorf("self-check failed: %w", err)
		}
		if _, err := proof.WriteTo(&proofBuf); err != nil {
			return err
		}
	default:
		pk, vk := plonk.NewProvingKey(ecc.BN254), plonk.NewVerifyingKey(ecc.BN254)
		if err := readFrom(*pkPath, pk); err != nil {
			return err
		}
		if err := readFrom(*vkPath, vk); err != nil {
			return err
		}
		proof, err := plonk.Prove(ccs, pk, full)
		if err != nil {
			return err
		}
		if err := plonk.Verify(proof, vk, pub); err != nil {
			return fmt.Errorf("self-check failed: %w", err)
		}
		if _, err := proof.WriteTo(&proofBuf); err != nil {
			return err
		}
	}

	pubBytes, err := pub.MarshalBinary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	enc := base64.StdEncoding
	if err := os.WriteFile(filepath.Join(*out, "public.b64"), []byte(enc.EncodeToString(pubBytes)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*out, "proof.b64"), []byte(enc.EncodeToString(proofBuf.Bytes())), 0o644)
}
