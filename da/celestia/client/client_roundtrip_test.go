package client

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"

	gsblob "github.com/celestiaorg/go-square/blob"
	"github.com/celestiaorg/go-square/inclusion"
	"github.com/celestiaorg/go-square/merkle"
	gsnamespace "github.com/celestiaorg/go-square/namespace"
	"github.com/filecoin-project/go-jsonrpc"
	"github.com/stretchr/testify/require"
)

type fakeBlobAPI struct {
	height uint64
	blobs  []*BlobJSON
	opts   *SubmitOptions
	data   []byte
	proof  BlobProofJSON
}

func (f *fakeBlobAPI) Submit(_ context.Context, blobs []*BlobJSON, opts *SubmitOptions) (uint64, error) {
	f.blobs = blobs
	f.opts = opts
	return f.height, nil
}

func (f *fakeBlobAPI) Get(_ context.Context, _ uint64, namespace Namespace, commitment Commitment) (*BlobJSON, error) {
	return &BlobJSON{Namespace: namespace, Data: f.data, Commitment: commitment}, nil
}

func (f *fakeBlobAPI) GetAll(_ context.Context, _ uint64, namespaces []Namespace) ([]*BlobJSON, error) {
	blobs := make([]*BlobJSON, 0, len(namespaces))
	for _, namespace := range namespaces {
		blobs = append(blobs, &BlobJSON{Namespace: namespace, Data: f.data})
	}
	return blobs, nil
}

func (f *fakeBlobAPI) GetProof(_ context.Context, _ uint64, _ Namespace, _ Commitment) (*BlobProofJSON, error) {
	return &f.proof, nil
}

func (f *fakeBlobAPI) Included(_ context.Context, _ uint64, _ Namespace, _ *BlobProofJSON, _ Commitment) (bool, error) {
	return true, nil
}

type fakeHeaderAPI struct{}

func (f *fakeHeaderAPI) GetByHeight(context.Context, uint64) (*ExtendedHeader, error) {
	return &ExtendedHeader{DAH: &DataAvailabilityHeader{}}, nil
}

type fakeStateAPI struct{}

func (f *fakeStateAPI) Balance(context.Context) (*Balance, error) {
	return &Balance{}, nil
}

func TestCreateCommitmentMatchesCelestia(t *testing.T) {
	namespace := make(Namespace, 29)
	copy(namespace[19:], []byte{0, 0, 0, 0, 0, 0, 0xff, 0xff, 0x12, 0x34})

	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 251)
	}

	commitment, err := createCommitment(namespace, data)
	require.NoError(t, err)
	require.Equal(t,
		"4fcaf2f22dfff79708ad2f8daa209349ad44f715bf209d3ae10956e20021450c",
		hex.EncodeToString(commitment),
	)
}

func TestCreateCommitmentMatchesCelestiaLargeBlob(t *testing.T) {
	namespace := make(Namespace, 29)
	copy(namespace[19:], []byte{0, 0, 0, 0, 0, 0, 0xff, 0xff, 0x12, 0x34})

	// 20,000 bytes spans > 32 shares (~42 shares).
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte(i % 251)
	}

	commitment, err := createCommitment(namespace, data)
	require.NoError(t, err)

	ns, err := gsnamespace.From(namespace)
	require.NoError(t, err)
	blob := gsblob.New(ns, data, 0)

	// Threshold 64 (ADR-013 subtreeRootThreshold) must match our commitment.
	expectedCommitment, err := inclusion.CreateCommitment(blob, merkle.HashFromByteSlices, 64)
	require.NoError(t, err)
	require.Equal(t, expectedCommitment, commitment)

	// Threshold 32 (the bug value) must NOT match.
	wrongCommitment, err := inclusion.CreateCommitment(blob, merkle.HashFromByteSlices, 32)
	require.NoError(t, err)
	require.NotEqual(t, wrongCommitment, commitment)
}

func TestClientBlobRoundTrip(t *testing.T) {
	blobAPI := &fakeBlobAPI{height: 42, proof: BlobProofJSON{}}
	rpcServer := jsonrpc.NewServer()
	rpcServer.Register("blob", blobAPI)
	rpcServer.Register("header", &fakeHeaderAPI{})
	rpcServer.Register("state", &fakeStateAPI{})
	server := httptest.NewServer(rpcServer)
	defer server.Close()

	namespace := make(Namespace, 29)
	namespace[0] = 0
	namespace[28] = 0xff
	data := []byte("rollapp batch payload")
	blobAPI.data = data

	client, err := NewClient(server.URL, "token")
	require.NoError(t, err)

	ctx := context.Background()
	ids, err := client.Submit(ctx, []Blob{data}, 0.1, namespace)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	height, commitment := SplitID(ids[0])
	require.Equal(t, uint64(42), height)
	require.Len(t, commitment, CommitmentLen)

	expectedCommitment, err := createCommitment(namespace, data)
	require.NoError(t, err)
	require.Equal(t, expectedCommitment, commitment)

	require.Len(t, blobAPI.blobs, 1)
	require.Equal(t, namespace, Namespace(blobAPI.blobs[0].Namespace))
	require.Equal(t, data, blobAPI.blobs[0].Data)
	require.Equal(t, uint8(0), blobAPI.blobs[0].ShareVersion)
	require.Equal(t, -1, blobAPI.blobs[0].Index)
	require.Equal(t, expectedCommitment, Commitment(blobAPI.blobs[0].Commitment))
	require.Equal(t, 0.1, blobAPI.opts.GasPrice)
	require.True(t, blobAPI.opts.IsGasPriceSet)

	blobs, err := client.Get(ctx, ids, namespace)
	require.NoError(t, err)
	require.Equal(t, []Blob{data}, blobs)

	proofs, err := client.GetProofs(ctx, ids, namespace)
	require.NoError(t, err)
	require.Len(t, proofs, 1)
	require.JSONEq(t, "[]", string(proofs[0]))

	included, err := client.Validate(ctx, ids, proofs, namespace)
	require.NoError(t, err)
	require.Equal(t, []bool{true}, included)

	// Validate with empty proof returns false without querying node
	includedEmpty, err := client.Validate(ctx, ids, []Proof{nil}, namespace)
	require.NoError(t, err)
	require.Equal(t, []bool{false}, includedEmpty)

	includedZeroLen, err := client.Validate(ctx, ids, []Proof{[]byte{}}, namespace)
	require.NoError(t, err)
	require.Equal(t, []bool{false}, includedZeroLen)

	// SubmitWithOptions
	_, err = client.SubmitWithOptions(ctx, []Blob{data}, 0.1, namespace, []byte("custom-options"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "custom submission options are not supported")

	idsWithOptions, err := client.SubmitWithOptions(ctx, []Blob{data}, 0.1, namespace, nil)
	require.NoError(t, err)
	require.Len(t, idsWithOptions, 1)

	// Malformed ID validation (commitment length != 32 bytes)
	malformedIDs := []ID{
		[]byte("short"),
		make([]byte, HeightLen),                 // missing commitment
		make([]byte, HeightLen+CommitmentLen-1), // 39 bytes
		make([]byte, HeightLen+CommitmentLen+1), // 41 bytes
	}
	for _, badID := range malformedIDs {
		_, err = client.Get(ctx, []ID{badID}, namespace)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid blob ID length")

		_, err = client.GetProofs(ctx, []ID{badID}, namespace)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid blob ID length")

		_, err = client.Validate(ctx, []ID{badID}, proofs, namespace)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid blob ID length")
	}

	// The blob module sends gas price through state.TxConfig's JSON shape.
	optionsJSON, err := json.Marshal(blobAPI.opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"gas_price":0.1,"is_gas_price_set":true}`, string(optionsJSON))
}
