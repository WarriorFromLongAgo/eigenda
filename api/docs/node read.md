这个文件的主要内容总结如下：
这是一个名为"node/node.proto"的协议缓冲区(Protocol Buffer)定义文件。
它定义了EigenDA网络中节点（Node）的各种消息类型和服务。
主要的消息类型包括Blob、BlobHeader、BatchHeader、Bundle等，用于描述数据块和批次的结构。
定义了两个主要服务：Dispersal（分散）和Retrieval（检索）。
Dispersal服务包含StoreChunks、StoreBlobs、AttestBatch和NodeInfo方法。
Retrieval服务包含RetrieveChunks、GetBlobHeader和NodeInfo方法。
文件还定义了一些辅助的消息类型，如MerkleProof和G2Commitment。






# 协议文档
<a name="top"></a>

## 目录

- [node/node.proto](#node_node-proto)
  - [AttestBatchReply](#node-AttestBatchReply)
  - [AttestBatchRequest](#node-AttestBatchRequest)
  - [BatchHeader](#node-BatchHeader)
  - [Blob](#node-Blob)
  - [BlobHeader](#node-BlobHeader)
  - [BlobQuorumInfo](#node-BlobQuorumInfo)
  - [Bundle](#node-Bundle)
  - [G2Commitment](#node-G2Commitment)
  - [GetBlobHeaderReply](#node-GetBlobHeaderReply)
  - [GetBlobHeaderRequest](#node-GetBlobHeaderRequest)
  - [MerkleProof](#node-MerkleProof)
  - [NodeInfoReply](#node-NodeInfoReply)
  - [NodeInfoRequest](#node-NodeInfoRequest)
  - [RetrieveChunksReply](#node-RetrieveChunksReply)
  - [RetrieveChunksRequest](#node-RetrieveChunksRequest)
  - [StoreBlobsReply](#node-StoreBlobsReply)
  - [StoreBlobsRequest](#node-StoreBlobsRequest)
  - [StoreChunksReply](#node-StoreChunksReply)
  - [StoreChunksRequest](#node-StoreChunksRequest)

  - [ChunkEncoding](#node-ChunkEncoding)

  - [Dispersal](#node-Dispersal)
  - [Retrieval](#node-Retrieval)

- [common/common.proto](#common_common-proto)
  - [G1Commitment](#common-G1Commitment)

- [标量值类型](#scalar-value-types)

<a name="node_node-proto"></a>
<p align="right"><a href="#top">顶部</a></p>

## node/node.proto

<a name="node-AttestBatchReply"></a>

### AttestBatchReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| signature | [bytes](#bytes) |  |  |

<a name="node-AttestBatchRequest"></a>

### AttestBatchRequest

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| batch_header | [BatchHeader](#node-BatchHeader) |  | 批次的头部 |
| blob_header_hashes | [bytes](#bytes) | repeated | 批次中所有数据块的头部哈希 |

<a name="node-BatchHeader"></a>

### BatchHeader
BatchHeader（参见 core/data.go#BatchHeader）

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| batch_root | [bytes](#bytes) |  | 以数据块头部哈希为叶子的默克尔树的根。 |
| reference_block_number | [uint32](#uint32) |  | 批次分散时的以太坊区块号。 |

<a name="node-Blob"></a>

### Blob
在EigenDA中，原始要分散的数据块通过取不同点的评估（即纠删码）编码为多项式。
这些点被分成不相交的子集，分配给EigenDA网络中的不同运营商节点。
此消息中的数据是分配给单个运营商节点的这些点的子集。

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| header | [BlobHeader](#node-BlobHeader) |  | 这是针对哪个（原始）数据块。 |
| bundles | [Bundle](#node-Bundle) | repeated | 每个bundle包含数据块的单个仲裁组的所有块。bundle的数量必须等于与数据块关联的仲裁组总数，并且顺序必须与BlobHeader.quorum_headers相同。注意：一个运营商可能在某些仲裁组中但不在所有仲裁组中；在这种情况下，对应该仲裁组的bundle将为空。 |

<a name="node-BlobHeader"></a>

### BlobHeader

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| commitment | [common.G1Commitment](#common-G1Commitment) |  | 表示数据块的多项式的KZG承诺。 |
| length_commitment | [G2Commitment](#node-G2Commitment) |  | G2上表示数据块的多项式的KZG承诺，用于证明多项式的度数 |
| length_proof | [G2Commitment](#node-G2Commitment) |  | 低度证明。它是将多项式移位到最大SRS度数的KZG承诺。 |
| length | [uint32](#uint32) |  | 原始数据块的长度，以符号数量表示（在定义多项式的字段中）。 |
| quorum_headers | [BlobQuorumInfo](#node-BlobQuorumInfo) | repeated | 此数据块参与的仲裁组的参数。 |
| account_id | [string](#string) |  | 将此数据块分散到EigenDA的用户的ID。 |
| reference_block_number | [uint32](#uint32) |  | 用于编码数据块的参考区块号 |

<a name="node-BlobQuorumInfo"></a>

### BlobQuorumInfo
参见api/proto/disperser/disperser.proto中定义的BlobQuorumParam

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| quorum_id | [uint32](#uint32) |  |  |
| adversary_threshold | [uint32](#uint32) |  |  |
| confirmation_threshold | [uint32](#uint32) |  |  |
| chunk_length | [uint32](#uint32) |  |  |
| ratelimit | [uint32](#uint32) |  |  |

<a name="node-Bundle"></a>

### Bundle
Bundle是与单个数据块、单个运营商和单个仲裁组相关联的块的集合。

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| chunks | [bytes](#bytes) | repeated | 每个块对应多项式上的一组点。每个块具有相同数量的点。 |
| bundle | [bytes](#bytes) |  | bundle的所有块编码在一个字节数组中。 |

<a name="node-G2Commitment"></a>

### G2Commitment

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| x_a0 | [bytes](#bytes) |  | G2点X坐标的A0元素。 |
| x_a1 | [bytes](#bytes) |  | G2点X坐标的A1元素。 |
| y_a0 | [bytes](#bytes) |  | G2点Y坐标的A0元素。 |
| y_a1 | [bytes](#bytes) |  | G2点Y坐标的A1元素。 |

<a name="node-GetBlobHeaderReply"></a>

### GetBlobHeaderReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| blob_header | [BlobHeader](#node-BlobHeader) |  | 根据GetBlobHeaderRequest请求的数据块的头部。 |
| proof | [MerkleProof](#node-MerkleProof) |  | 证明返回的数据块头部属于批次并且是批次的第MerkleProof.index个数据块的默克尔证明。这可以根据链上的批次根进行检查。 |

<a name="node-GetBlobHeaderRequest"></a>

### GetBlobHeaderRequest
有关GetBlobHeaderRequest每个参数的文档，请参见RetrieveChunksRequest。

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| batch_header_hash | [bytes](#bytes) |  |  |
| blob_index | [uint32](#uint32) |  |  |
| quorum_id | [uint32](#uint32) |  |  |

<a name="node-MerkleProof"></a>

### MerkleProof

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| hashes | [bytes](#bytes) | repeated | 证明本身。 |
| index | [uint32](#uint32) |  | 此证明针对的索引（默克尔树的叶子）。 |

<a name="node-NodeInfoReply"></a>

### NodeInfoReply
节点信息回复

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| semver | [string](#string) |  |  |
| arch | [string](#string) |  |  |
| os | [string](#string) |  |  |
| num_cpu | [uint32](#uint32) |  |  |
| mem_bytes | [uint64](#uint64) |  |  |

<a name="node-NodeInfoRequest"></a>

### NodeInfoRequest
节点信息请求

<a name="node-RetrieveChunksReply"></a>

### RetrieveChunksReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| chunks | [bytes](#bytes) | repeated | 节点为RetrieveChunksRequest请求的数据块存储的所有块。 |
| encoding | [ChunkEncoding](#node-ChunkEncoding) |  | 上述块的编码方式。 |

<a name="node-RetrieveChunksRequest"></a>

### RetrieveChunksRequest

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| batch_header_hash | [bytes](#bytes) |  | 链上定义的ReducedBatchHeader的哈希，参见：https://github.com/Layr-Labs/eigenda/blob/master/contracts/src/interfaces/IEigenDAServiceManager.sol#L43 这标识要检索的批次。 |
| blob_index | [uint32](#uint32) |  | 要检索的批次中的哪个数据块（注意：批次在逻辑上是数据块的有序列表）。 |
| quorum_id | [uint32](#uint32) |  | 要检索的数据块的哪个仲裁组（注意：一个数据块可以有多个仲裁组，节点上不同仲裁组的块可能不同）。ID必须在[0, 254]范围内。 |

<a name="node-StoreBlobsReply"></a>

### StoreBlobsReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| signatures | [google.protobuf.BytesValue](#google-protobuf-BytesValue) | repeated | 运营商对数据块头部哈希签名的BLS签名。签名的顺序必须与请求中发送的数据块顺序匹配，丢弃的数据块位置使用空签名。 |

<a name="node-StoreBlobsRequest"></a>

### StoreBlobsRequest

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| blobs | [Blob](#node-Blob) | repeated | 要存储的数据块 |
| reference_block_number | [uint32](#uint32) |  | 用于编码数据块的参考区块号 |

<a name="node-StoreChunksReply"></a>

### StoreChunksReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| signature | [bytes](#bytes) |  | 运营商对批
