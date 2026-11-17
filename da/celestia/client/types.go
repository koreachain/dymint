package client

import (
	"context"
	"time"

	"github.com/celestiaorg/nmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DAClient defines very generic interface for interaction with Data Availability layers.
type DAClient interface {
	// Get returns Blob for each given ID, or an error.
	//
	// Error should be returned if ID is not formatted properly, there is no Blob for given ID or any other client-level
	// error occurred (dropped connection, timeout, etc).
	Get(ctx context.Context, ids []ID, namespace Namespace) ([]Blob, error)

	// GetIDs returns IDs of all Blobs located in DA at given height.
	GetIDs(ctx context.Context, height uint64, namespace Namespace) (*GetIDsResult, error)

	// GetProofs returns inclusion Proofs for Blobs specified by their IDs.
	GetProofs(ctx context.Context, ids []ID, namespace Namespace) ([]Proof, error)

	// Commit creates a Commitment for each given Blob.
	Commit(ctx context.Context, blobs []Blob, namespace Namespace) ([]Commitment, error)

	// Submit submits the Blobs to Data Availability layer.
	//
	// This method is synchronous. Upon successful submission to Data Availability layer, it returns the IDs identifying blobs
	// in DA.
	Submit(ctx context.Context, blobs []Blob, gasPrice float64, namespace Namespace) ([]ID, error)

	// SubmitWithOptions submits the Blobs to Data Availability layer.
	//
	// This method is synchronous. Upon successful submission to Data Availability layer, it returns the IDs identifying blobs
	// in DA.
	SubmitWithOptions(ctx context.Context, blobs []Blob, gasPrice float64, namespace Namespace, options []byte) ([]ID, error)

	// Validate validates Commitments against the corresponding Proofs. This should be possible without retrieving the Blobs.
	Validate(ctx context.Context, ids []ID, proofs []Proof, namespace Namespace) ([]bool, error)

	// GetByHeight retrieves celestia extended header by height
	GetByHeight(ctx context.Context, height uint64) (*ExtendedHeader, error)

	// Balance returns the celestia light client account balance
	Balance(context.Context) (*Balance, error)

	// MaxBlobSize returns the max blob size allowed in celestia
	MaxBlobSize(ctx context.Context) (uint64, error)
}

// Namespace is an optional parameter used to set the location a blob should be
// posted to, for DA layers supporting the functionality.
type Namespace = []byte

// Blob is the data submitted/received from DA interface.
type Blob = []byte

// ID should contain serialized data required by the implementation to find blob in Data Availability layer.
type ID = []byte

// Commitment should contain serialized cryptographic commitment to Blob value.
type Commitment = []byte

// Proof should contain serialized proof of inclusion (publication) of Blob in Data Availability layer.
type Proof = []byte

// Balance is an alias to the Coin type from Cosmos-SDK.
type Balance = sdk.Coin

// GetIDsResult holds the result of GetIDs call: IDs and timestamp of corresponding block.
type GetIDsResult struct {
	IDs       []ID
	Timestamp time.Time
}

// BlobJSON is the JSON representation of a blob accepted by celestia-node's blob module.
type BlobJSON struct {
	Namespace    Namespace  `json:"namespace"`
	Data         []byte     `json:"data"`
	ShareVersion uint8      `json:"share_version"`
	Commitment   Commitment `json:"commitment,omitempty"`
	Signer       []byte     `json:"signer,omitempty"`
	Index        int        `json:"index"`
}

// BlobProofJSON is the JSON representation of a blob inclusion proof returned by the blob module.
type BlobProofJSON []nmt.Proof

// SubmitOptions mirrors celestia-node's blob.SubmitOptions, which is an alias of state.TxConfig.
type SubmitOptions struct {
	GasPrice      float64 `json:"gas_price,omitempty"`
	IsGasPriceSet bool    `json:"is_gas_price_set,omitempty"`
	MaxGasPrice   float64 `json:"max_gas_price,omitempty"`
	Gas           uint64  `json:"gas,omitempty"`
}

// BlobAPI defines the jsonrpc blob service module API.
type BlobAPI struct {
	Internal struct {
		Submit   func(context.Context, []*BlobJSON, *SubmitOptions) (uint64, error)                 `perm:"write"`
		Get      func(context.Context, uint64, Namespace, Commitment) (*BlobJSON, error)            `perm:"read"`
		GetAll   func(context.Context, uint64, []Namespace) ([]*BlobJSON, error)                    `perm:"read"`
		GetProof func(context.Context, uint64, Namespace, Commitment) (*BlobProofJSON, error)       `perm:"read"`
		Included func(context.Context, uint64, Namespace, *BlobProofJSON, Commitment) (bool, error) `perm:"read"`
	}
}

// Submit submits the given blobs to the DA layer and returns the height they were included at.
func (api *BlobAPI) Submit(ctx context.Context, blobs []*BlobJSON, options *SubmitOptions) (uint64, error) {
	return api.Internal.Submit(ctx, blobs, options)
}

// Get returns the blob at the given height, namespace and commitment.
func (api *BlobAPI) Get(ctx context.Context, height uint64, ns Namespace, commitment Commitment) (*BlobJSON, error) {
	return api.Internal.Get(ctx, height, ns, commitment)
}

// GetAll returns all blobs at the given height under the given namespaces.
func (api *BlobAPI) GetAll(ctx context.Context, height uint64, ns []Namespace) ([]*BlobJSON, error) {
	return api.Internal.GetAll(ctx, height, ns)
}

// GetProof returns the inclusion proof for the blob at the given height, namespace and commitment.
func (api *BlobAPI) GetProof(ctx context.Context, height uint64, ns Namespace, commitment Commitment) (*BlobProofJSON, error) {
	return api.Internal.GetProof(ctx, height, ns, commitment)
}

// Included checks whether the blob is included at the given height, namespace and commitment.
func (api *BlobAPI) Included(ctx context.Context, height uint64, ns Namespace, proof *BlobProofJSON, commitment Commitment) (bool, error) {
	return api.Internal.Included(ctx, height, ns, proof, commitment)
}

// API defines the jsonrpc Header service module API
type HeadersAPI struct {
	Internal struct {
		GetByHeight func(context.Context, uint64) (*ExtendedHeader, error) `perm:"read"`
	}
}

// GetByHeight submits the Blobs to Data Availability layer.
func (api *HeadersAPI) GetByHeight(ctx context.Context, height uint64) (*ExtendedHeader, error) {
	return api.Internal.GetByHeight(ctx, height)
}

// API defines the jsonrpc service module API
type StateAPI struct {
	Internal struct {
		Balance func(context.Context) (*Balance, error) `perm:"read"`
	}
}

// GetByHeight submits the Blobs to Data Availability layer.
func (api *StateAPI) Balance(ctx context.Context) (*Balance, error) {
	return api.Internal.Balance(ctx)
}
