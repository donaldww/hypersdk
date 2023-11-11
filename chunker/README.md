Gossip Chunks to other validators. Desire to see a Chunk included
is based on validator's ability to distribute (no regossip by other
validators). Don't include transactions we have already seen in other
chunks in a node's produced chunks. We include a signature so that we can
penalize producers that send multiple chunks at the same height.
```
type Chunk struct {
    Signer: BLSPublicKey,
    Signature: BLSSignature,

    Height: uint64,
    Transactions: [
        <Transaction>,
        ...,
    ],
}
```

If we are missing a chunk from someone, we can request it (if height, it
is from a particular builder):
```
type ChunkRequest struct {
    Height: uint64,
    Chunk: ids.ID,
}
```

Only reply if all transactions are well-formatted, have valid async signatures. If not,
penalize sender.
```
type ChunkResponse struct {
    Signer: BLSPublicKey,
    Signature: BLSSignature,

    Chunk: ids.ID,
}
```

Pushing signatures allows anyone to include a `Chunk` in their `Block`.
```
type Signature struct {
    Signers: BitSet,
    Signature: BLSSignature,

    Chunk: ids.ID,
}
```

By filtering pre-consensus data, we get the best of both worlds. We can take advantage
of pre-consensus data distribution but not be beholden to the inefficiencies of it.
```
type Block struct {
    Timestamp: uint64,
    ParentBlock: ids.ID,
    ParentRoot: ids.ID,
    Height: uint64,
    Chunks: [
        <OriginalChunkID, SignatureBitSet, Signature, FilteredChunkID, WarpBitSet>,
        ...,
    ],
}
```

Each validator can store Y processing chunks on the network for potential inclusion. If a chunk contains no valid transacitons,
it can be included in a block as a "delete" so that the validator doesn't need to wait the entire timeout for inclusion (and doesn't need
to waste the work of iterating over it during building).

Insight: Filter chunks we agreed on data availability of because they will undoubtedly contain unexecutable transactions (fee exceeds max, user
runs out of funds). Store OriginalChunkIDs until chain time surpasses latest tx or X blocks after it was accepted. Then we only store FilteredChunkID.
New nodes syncing only need to fetch FilteredChunkID. This differs slightly from Narwhal/Tusk/Bullshark, which try to use the chunks directly in consensus.

Other Benefit: at scale, chunk broadcaster and chunk receiver can be decoupled from the node (similar to Narwhal/Tusk).

## Max Throughput Estimates
### Parameters
Validators = 2000
Average Tx Size = 400B
Max Chunk Size = 2MB
Max Warp Messages Per Chunk = 64

### Max Calculations
Max Size Per Block Chunk = <32, 2000/8, 96, 32, 64/8> = 418B
Max Chunks Per Block = 2MB/418B = 4785 Chunks
Max Txs Per Block = 4785 * 2MB/400B = 23.9M
Max Data Bandwidth Finalized Per Block = 4785 * 2MB = 9.57GB (76.56 Gb)

### 300k Tx/Block Calculations
Data Bandwidth Required = 120MB (0.96 Gb)
Block Size (tightly packed chunks) = 418B * 60 = 25KiB
Block Size (20% executable/full) = 418B * 300 = 125KiB (80% savings on long-term block data storage)

## Open Questions
* To minimize duplicate txs that can be issued by a single address, we require that addresses be sent (from non-validators) over P2P
to a specific issuer for a specific expiry time. May want to remove non-validator -> validator P2P gossip entirely?
-> Validators that don't want to distribute any of their own transactions end up having a very low outbound traffic requirement.