这个文件的主要内容总结如下：
这是一个名为"retriever/retriever.proto"的协议缓冲区(Protocol Buffer)定义文件。
它定义了一个名为"Retriever"的服务，用于从EigenDA运营商节点检索数据块（blob）的块并重构原始数据块。
文件定义了两个主要的消息类型：BlobRequest和BlobReply。
Retriever服务有一个名为"RetrieveBlob"的RPC方法，用于检索和重构数据块。
文件还解释了用户从EigenDA检索数据块的两种方式，并比较了它们的优缺点。
文件包含了一些通用的定义，如G1Commitment。





# 协议文档
<a name="top"></a>

## 目录

- [retriever/retriever.proto](#retriever_retriever-proto)
    - [BlobReply](#retriever-BlobReply)
    - [BlobRequest](#retriever-BlobRequest)

    - [Retriever](#retriever-Retriever)

- [common/common.proto](#common_common-proto)
    - [G1Commitment](#common-G1Commitment)

- [标量值类型](#scalar-value-types)

<a name="retriever_retriever-proto"></a>
<p align="right"><a href="#top">顶部</a></p>

## retriever/retriever.proto

<a name="retriever-BlobReply"></a>

### BlobReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| data | [bytes](#bytes) |  | 根据BlobRequest从EigenDA节点检索并重构的数据块。 |

<a name="retriever-BlobRequest"></a>

### BlobRequest

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| batch_header_hash | [bytes](#bytes) |  | 链上定义的ReducedBatchHeader的哈希，参见：https://github.com/Layr-Labs/eigenda/blob/master/contracts/src/interfaces/IEigenDAServiceManager.sol#L43 这标识了该数据块所属的批次。 |
| blob_index | [uint32](#uint32) |  | 这是请求批次中的哪个数据块（注意：批次在逻辑上是数据块的有序列表）。 |
| reference_block_number | [uint32](#uint32) |  | 构建此数据块的批次时的以太坊区块号。 |
| quorum_id | [uint32](#uint32) |  | 这是请求数据块的哪个仲裁组（注意：一个数据块可以参与多个仲裁组）。 |

<a name="retriever-Retriever"></a>

### Retriever
Retriever是一个用于从EigenDA运营商节点检索对应于数据块的块并从这些块重构原始数据块的服务。
这是一个客户端库，用户应该将其操作化。

注意：用户通常有两种方式从EigenDA检索数据块：
1) 从用户最初用于分散的Disperser检索：API是Disperser.RetrieveBlob()，定义在api/proto/disperser/disperser.proto中
2) 直接从EigenDA节点检索，这由此Retriever支持。

Disperser.RetrieveBlob()（第1种方法）通常更快且更便宜，因为Disperser管理它处理过的数据块，
而Retriever.RetrieveBlob()（这里的第2种方法）消除了对Disperser的信任需求，但代价是成本和性能较差。

| 方法名 | 请求类型 | 响应类型 | 描述 |
| ----------- | ------------ | ------------- | ------------|
| RetrieveBlob | [BlobRequest](#retriever-BlobRequest) | [BlobReply](#retriever-BlobReply) | 这会向EigenDA节点发出扇出请求以检索块，并在响应中返回重构的原始数据块。 |

<a name="common_common-proto"></a>


