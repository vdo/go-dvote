package vochain

// go test -benchmem -run=^$ -bench=. -cpu=10

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"sync/atomic"

	abcitypes "github.com/tendermint/tendermint/abci/types"
	"gitlab.com/vocdoni/go-dvote/crypto/ethereum"
	"gitlab.com/vocdoni/go-dvote/crypto/snarks"
	tree "gitlab.com/vocdoni/go-dvote/trie"
	"gitlab.com/vocdoni/go-dvote/types"
	"gitlab.com/vocdoni/go-dvote/util"
)

func BenchmarkCheckTx(b *testing.B) {
	b.ReportAllocs()
	app, err := NewBaseApplication(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	var voters [][]*types.VoteTx
	for i := 0; i < b.N+1; i++ {
		voters = append(voters, prepareBenchCheckTx(b, app, 1000))
		b.Logf("creating process %s", voters[i][0].ProcessID)
	}
	var i int32
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			b.Logf("Running vote %d", i)
			benchCheckTx(b, app, voters[atomic.AddInt32(&i, 1)])
		}
	})
}

func prepareBenchCheckTx(b *testing.B, app *BaseApplication, nvoters int) (voters []*types.VoteTx) {
	tr, err := tree.NewTree("checkTXbench", b.TempDir())
	if err != nil {
		b.Fatal(err)
	}

	keys := createEthRandomKeysBatch(nvoters)
	if keys == nil {
		b.Fatal("cannot create keys batch")
	}
	claims := []string{}
	for _, k := range keys {
		pub, _ := k.HexString()
		pub, err = ethereum.DecompressPubKey(pub)
		if err != nil {
			b.Fatal(err)
		}
		pubb, err := hex.DecodeString(pub)
		if err != nil {
			b.Fatal(err)
		}
		c := snarks.Poseidon.Hash(pubb)
		tr.AddClaim(c, nil)
		claims = append(claims, string(c))
	}
	process := &types.Process{
		StartBlock:     0,
		Type:           types.PollVote,
		EntityID:       util.RandomBytes(types.EntityIDsize),
		MkRoot:         tr.Root(),
		NumberOfBlocks: 1024,
	}
	pid := util.RandomBytes(types.ProcessIDsize)
	app.State.AddProcess(*process, pid, "ipfs://123456789")

	var proof string

	for i, s := range keys {
		proof, err = tr.GenProof([]byte(claims[i]), nil)
		if err != nil {
			b.Fatal(err)
		}
		tx := types.VoteTx{
			Nonce:     util.RandomHex(16),
			ProcessID: hex.EncodeToString(pid),
			Proof:     proof,
		}

		txBytes, err := json.Marshal(tx)
		if err != nil {
			b.Fatal(err)
		}
		if tx.Signature, err = s.Sign(txBytes); err != nil {
			b.Fatal(err)
		}
		tx.Type = "vote"
		voters = append(voters, &tx)
	}
	return voters
}

func benchCheckTx(b *testing.B, app *BaseApplication, voters []*types.VoteTx) {
	var cktx abcitypes.RequestCheckTx
	var detx abcitypes.RequestDeliverTx

	var cktxresp abcitypes.ResponseCheckTx
	var detxresp abcitypes.ResponseDeliverTx

	var err error
	var txBytes []byte

	i := 0
	for _, tx := range voters {
		if txBytes, err = json.Marshal(tx); err != nil {
			b.Fatal(err)
		}
		cktx.Tx = txBytes
		cktxresp = app.CheckTx(cktx)
		if cktxresp.Code != 0 {
			b.Fatalf(fmt.Sprintf("checkTX failed: %s", cktxresp.Data))
		} else {
			detx.Tx = txBytes
			detxresp = app.DeliverTx(detx)
			if detxresp.Code != 0 {
				b.Fatalf(fmt.Sprintf("deliverTX failed: %s", detxresp.Data))
			}
		}
		i++
		if i%100 == 0 {
			app.Commit()
		}
	}
	app.Commit()
}
