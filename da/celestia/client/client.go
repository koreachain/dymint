package client

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/filecoin-project/go-jsonrpc"

	gsblob "github.com/celestiaorg/go-square/blob"
	"github.com/celestiaorg/go-square/inclusion"
	"github.com/celestiaorg/go-square/merkle"
	gsnamespace "github.com/celestiaorg/go-square/namespace"
)

// HeightLen is a length (in bytes) of serialized height embedded in a blob ID.
const HeightLen = 8

// CommitmentLen is the length (in bytes) of a Celestia share commitment (SHA-256 hash).
const CommitmentLen = 32

// subtreeRootThreshold is the ADR-013 subtree root threshold used to compute
// share commitments. It matches celestia-app's appconsts.SubtreeRootThreshold.
const subtreeRootThreshold = 64

// Client is the jsonrpc client
type Client struct {
	Blobs   BlobAPI
	Headers HeadersAPI
	State   StateAPI
	closer  multiClientCloser
}

// Balance implements DAClient.
func (c Client) Balance(ctx context.Context) (*Balance, error) {
	return c.State.Balance(ctx)
}

// Commit implements DAClient.
func (c Client) Commit(ctx context.Context, blobs []Blob, namespace Namespace) ([]Commitment, error) {
	commitments := make([]Commitment, len(blobs))
	for i, blob := range blobs {
		commitment, err := createCommitment(namespace, blob)
		if err != nil {
			return nil, fmt.Errorf("create commitment: %w", err)
		}
		commitments[i] = commitment
	}
	return commitments, nil
}

// Get implements DAClient.
func (c Client) Get(ctx context.Context, ids []ID, namespace Namespace) ([]Blob, error) {
	blobs := make([]Blob, len(ids))
	for i, id := range ids {
		height, commitment := SplitID(id)
		if len(commitment) != CommitmentLen {
			return nil, fmt.Errorf("invalid blob ID length %d: expected %d bytes (8-byte height + 32-byte commitment)", len(id), HeightLen+CommitmentLen)
		}
		blob, err := c.Blobs.Get(ctx, height, namespace, commitment)
		if err != nil {
			return nil, err
		}
		if blob == nil {
			return nil, fmt.Errorf("blob not found at height %d", height)
		}
		blobs[i] = blob.Data
	}
	return blobs, nil
}

// GetByHeight implements DAClient.
func (c Client) GetByHeight(ctx context.Context, height uint64) (*ExtendedHeader, error) {
	return c.Headers.GetByHeight(ctx, height)
}

// GetIDs implements DAClient.
func (c Client) GetIDs(ctx context.Context, height uint64, namespace Namespace) (*GetIDsResult, error) {
	blobs, err := c.Blobs.GetAll(ctx, height, []Namespace{namespace})
	if err != nil {
		return nil, err
	}
	result := &GetIDsResult{IDs: make([]ID, 0, len(blobs))}
	for _, blob := range blobs {
		if blob == nil {
			continue
		}
		result.IDs = append(result.IDs, MakeID(height, blob.Commitment))
	}
	return result, nil
}

// GetProofs implements DAClient.
func (c Client) GetProofs(ctx context.Context, ids []ID, namespace Namespace) ([]Proof, error) {
	proofs := make([]Proof, len(ids))
	for i, id := range ids {
		height, commitment := SplitID(id)
		if len(commitment) != CommitmentLen {
			return nil, fmt.Errorf("invalid blob ID length %d: expected %d bytes (8-byte height + 32-byte commitment)", len(id), HeightLen+CommitmentLen)
		}
		proof, err := c.Blobs.GetProof(ctx, height, namespace, commitment)
		if err != nil {
			return nil, err
		}
		if proof == nil {
			continue
		}
		proofJSON, err := json.Marshal(proof)
		if err != nil {
			return nil, fmt.Errorf("marshal proof: %w", err)
		}
		proofs[i] = proofJSON
	}
	return proofs, nil
}

// MaxBlobSize implements DAClient.
func (c Client) MaxBlobSize(context.Context) (uint64, error) {
	return 0, errors.New("max blob size is not exposed by celestia-node's blob module")
}

// Submit implements DAClient.
func (c Client) Submit(ctx context.Context, blobs []Blob, gasPrice float64, namespace Namespace) ([]ID, error) {
	nodeBlobs := make([]*BlobJSON, len(blobs))
	commitments := make([]Commitment, len(blobs))
	for i, blob := range blobs {
		commitment, err := createCommitment(namespace, blob)
		if err != nil {
			return nil, fmt.Errorf("create commitment: %w", err)
		}
		commitments[i] = commitment
		nodeBlobs[i] = &BlobJSON{
			Namespace:    namespace,
			Data:         blob,
			ShareVersion: 0,
			Commitment:   commitment,
			Index:        -1,
		}
	}

	height, err := c.Blobs.Submit(ctx, nodeBlobs, &SubmitOptions{
		GasPrice:      gasPrice,
		IsGasPriceSet: true,
	})
	if err != nil {
		return nil, err
	}

	ids := make([]ID, len(commitments))
	for i, commitment := range commitments {
		ids[i] = MakeID(height, commitment)
	}
	return ids, nil
}

// SubmitWithOptions implements DAClient.
func (c Client) SubmitWithOptions(ctx context.Context, blobs []Blob, gasPrice float64, namespace Namespace, options []byte) ([]ID, error) {
	if len(options) > 0 {
		return nil, errors.New("custom submission options are not supported by celestia blob module")
	}
	return c.Submit(ctx, blobs, gasPrice, namespace)
}

// Validate implements DAClient.
func (c Client) Validate(ctx context.Context, ids []ID, proofs []Proof, namespace Namespace) ([]bool, error) {
	if len(ids) != len(proofs) {
		return nil, fmt.Errorf("ids and proofs length mismatch: %d != %d", len(ids), len(proofs))
	}

	included := make([]bool, len(ids))
	for i, id := range ids {
		height, commitment := SplitID(id)
		if len(commitment) != CommitmentLen {
			return nil, fmt.Errorf("invalid blob ID length %d: expected %d bytes (8-byte height + 32-byte commitment)", len(id), HeightLen+CommitmentLen)
		}
		if len(proofs[i]) == 0 {
			included[i] = false
			continue
		}
		var proof BlobProofJSON
		err := json.Unmarshal(proofs[i], &proof)
		if err != nil {
			return nil, fmt.Errorf("unmarshal proof: %w", err)
		}
		if proof == nil {
			included[i] = false
			continue
		}
		ok, err := c.Blobs.Included(ctx, height, namespace, &proof, commitment)
		if err != nil {
			return nil, err
		}
		included[i] = ok
	}
	return included, nil
}

// createCommitment computes the share commitment of a v0 blob using the same
// ADR-013 rules as celestia-node.
func createCommitment(namespace Namespace, data Blob) (Commitment, error) {
	ns, err := gsnamespace.From(namespace)
	if err != nil {
		return nil, fmt.Errorf("namespace: %w", err)
	}
	blob := gsblob.New(ns, data, 0)
	return inclusion.CreateCommitment(blob, merkle.HashFromByteSlices, subtreeRootThreshold)
}

// MakeID builds a blob ID from its height and share commitment.
func MakeID(height uint64, commitment Commitment) ID {
	id := make([]byte, HeightLen+len(commitment))
	binary.LittleEndian.PutUint64(id, height)
	copy(id[HeightLen:], commitment)
	return id
}

// SplitID returns the height and share commitment encoded in a blob ID.
func SplitID(id ID) (uint64, Commitment) {
	if len(id) <= HeightLen {
		return 0, nil
	}
	return binary.LittleEndian.Uint64(id[:HeightLen]), append([]byte(nil), id[HeightLen:]...)
}

// multiClientCloser is a wrapper struct to close clients across multiple namespaces.
type multiClientCloser struct {
	closers []jsonrpc.ClientCloser
}

// register adds a new closer to the multiClientCloser
func (m *multiClientCloser) register(closer jsonrpc.ClientCloser) {
	m.closers = append(m.closers, closer)
}

// closeAll closes all saved clients.
func (m *multiClientCloser) closeAll() {
	for _, closer := range m.closers {
		closer()
	}
}

// Close closes the connections to all namespaces registered on the staticClient.
func (c *Client) Close() {
	c.closer.closeAll()
}

// NewClient returns a DA backend based on the uri
// and auth token.
func NewClient(uri, token string) (DAClient, error) {
	var client DAClient

	authHeader := http.Header{"Authorization": []string{fmt.Sprintf("Bearer %s", token)}}

	jsonrpcClient, err := newClient(context.Background(), uri, authHeader)
	if err != nil {
		return nil, err
	}
	client = *jsonrpcClient

	return client, nil
}

func newClient(ctx context.Context, addr string, authHeader http.Header) (*Client, error) {
	var multiCloser multiClientCloser
	var client Client
	errs := getKnownErrorsMapping()
	for name, module := range moduleMap(&client) {
		closer, err := jsonrpc.NewMergeClient(ctx, addr, name, []interface{}{module}, authHeader, jsonrpc.WithErrors(errs))
		if err != nil {
			return nil, err
		}
		multiCloser.register(closer)
	}
	client.closer = multiCloser

	return &client, nil
}

func moduleMap(client *Client) map[string]interface{} {
	// TODO: this duplication of strings many times across the codebase can be avoided with issue #1176
	return map[string]interface{}{
		"blob":   &client.Blobs.Internal,
		"header": &client.Headers.Internal,
		"state":  &client.State.Internal,
	}
}

// getKnownErrorsMapping returns a mapping of known error codes to their corresponding error types.
func getKnownErrorsMapping() jsonrpc.Errors {
	errs := jsonrpc.NewErrors()
	errs.Register(jsonrpc.ErrorCode(CodeBlobNotFound), new(*ErrBlobNotFound))
	errs.Register(jsonrpc.ErrorCode(CodeBlobSizeOverLimit), new(*ErrBlobSizeOverLimit))
	errs.Register(jsonrpc.ErrorCode(CodeTxTimedOut), new(*ErrTxTimedOut))
	errs.Register(jsonrpc.ErrorCode(CodeTxAlreadyInMempool), new(*ErrTxAlreadyInMempool))
	errs.Register(jsonrpc.ErrorCode(CodeTxIncorrectAccountSequence), new(*ErrTxIncorrectAccountSequence))
	errs.Register(jsonrpc.ErrorCode(CodeTxTooLarge), new(*ErrTxTooLarge))
	errs.Register(jsonrpc.ErrorCode(CodeContextDeadline), new(*ErrContextDeadline))
	errs.Register(jsonrpc.ErrorCode(CodeFutureHeight), new(*ErrFutureHeight))
	errs.Register(jsonrpc.ErrorCode(CodeHeightZero), new(*ErrHeightZero))
	return errs
}
