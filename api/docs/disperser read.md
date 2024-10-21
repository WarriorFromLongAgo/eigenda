这个文件的主要内容总结如下：
这是一个名为"disperser/disperser.proto"的协议缓冲区(Protocol Buffer)定义文件。
它定义了一个名为"Disperser"的服务，用于处理数据块（blob）的分散和检索。
文件中定义了多个消息类型，如DisperseBlobRequest、DisperseBlobReply、BlobStatusRequest等。
定义了一个BlobStatus枚举类型，表示数据块的不同状态。
Disperser服务提供了四个主要方法：DisperseBlob、DisperseBlobAuthenticated、GetBlobStatus和RetrieveBlob。
文件还包含了一些与数据认证、批处理和验证相关的消息类型。






# 协议文档
<a name="top"></a>

## 目录

- [disperser/disperser.proto](#disperser_disperser-proto)
  - [AuthenticatedReply](#disperser-AuthenticatedReply)
  - [AuthenticatedRequest](#disperser-AuthenticatedRequest)
  - [AuthenticationData](#disperser-AuthenticationData)
  - [BatchHeader](#disperser-BatchHeader)
  - [BatchMetadata](#disperser-BatchMetadata)
  - [BlobAuthHeader](#disperser-BlobAuthHeader)
  - [BlobHeader](#disperser-BlobHeader)
  - [BlobInfo](#disperser-BlobInfo)
  - [BlobQuorumParam](#disperser-BlobQuorumParam)
  - [BlobStatusReply](#disperser-BlobStatusReply)
  - [BlobStatusRequest](#disperser-BlobStatusRequest)
  - [BlobVerificationProof](#disperser-BlobVerificationProof)
  - [DisperseBlobReply](#disperser-DisperseBlobReply)
  - [DisperseBlobRequest](#disperser-DisperseBlobRequest)
  - [RetrieveBlobReply](#disperser-RetrieveBlobReply)
  - [RetrieveBlobRequest](#disperser-RetrieveBlobRequest)

  - [BlobStatus](#disperser-BlobStatus)

  - [Disperser](#disperser-Disperser)

- [common/common.proto](#common_common-proto)
  - [G1Commitment](#common-G1Commitment)

- [标量值类型](#scalar-value-types)

<a name="disperser_disperser-proto"></a>
<p align="right"><a href="#top">顶部</a></p>

## disperser/disperser.proto

<a name="disperser-AuthenticatedReply"></a>

### AuthenticatedReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| blob_auth_header | [BlobAuthHeader](#disperser-BlobAuthHeader) |  |  |
| disperse_reply | [DisperseBlobReply](#disperser-DisperseBlobReply) |  |  |

<a name="disperser-AuthenticatedRequest"></a>

### AuthenticatedRequest

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| disperse_request | [DisperseBlobRequest](#disperser-DisperseBlobRequest) |  |  |
| authentication_data | [AuthenticationData](#disperser-AuthenticationData) |  |  |

<a name="disperser-AuthenticationData"></a>

### AuthenticationData
AuthenticationData包含BlobAuthHeader的签名。

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| authentication_data | [bytes](#bytes) |  |  |

<a name="disperser-BatchHeader"></a>

### BatchHeader

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| batch_root | [bytes](#bytes) |  | 以数据块头哈希为叶子节点的默克尔树的根。 |
| quorum_numbers | [bytes](#bytes) |  | 与此批次中的数据块相关的所有仲裁组。按升序排列。例如：[0, 2, 1] => 0x000102 |
| quorum_signed_percentages | [bytes](#bytes) |  | 已为此批次签名的权益百分比。quorum_signed_percentages[i]是quorum_numbers[i]的百分比。 |
| reference_block_number | [uint32](#uint32) |  | 创建批次时的以太坊区块号。分散器将根据此区块号的链上信息（如运营商权益）对数据块进行编码和分散。 |

<a name="disperser-BatchMetadata"></a>

### BatchMetadata

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| batch_header | [BatchHeader](#disperser-BatchHeader) |  |  |
| signatory_record_hash | [bytes](#bytes) |  | 未签署批次的所有运营商公钥的哈希。 |
| fee | [bytes](#bytes) |  | 用户为分散此批次支付的费用。它是big.Int值的字节表示。 |
| confirmation_block_number | [uint32](#uint32) |  | 批次在链上确认的以太坊区块号。 |
| batch_header_hash | [bytes](#bytes) |  | 这是链上定义的ReducedBatchHeader的哈希，参见：https://github.com/Layr-Labs/eigenda/blob/master/contracts/src/interfaces/IEigenDAServiceManager.sol#L43 这是运营商将在其上签名的消息。 |

<a name="disperser-BlobAuthHeader"></a>

### BlobAuthHeader
BlobAuthHeader包含客户端验证和签名数据块的信息。
- 一旦启用支付，BlobAuthHeader将包含数据块的KZG承诺，客户端将对其进行验证和签名。让客户端验证KZG承诺而不是计算它避免了客户端需要KZG结构化参考字符串（SRS），这可能很大。
  签名的KZG承诺防止分散器向DA节点发送与客户端发送的不同的数据块。
- 同时，BlobAuthHeader包含一个简单的挑战参数，用于防止在签名泄露的情况下进行重放攻击。

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| challenge_parameter | [uint32](#uint32) |  |  |

<a name="disperser-BlobHeader"></a>

### BlobHeader

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| commitment | [common.G1Commitment](#common-G1Commitment) |  | 数据块的KZG承诺。 |
| data_length | [uint32](#uint32) |  | 数据块的长度（以符号为单位，每个符号32字节）。 |
| blob_quorum_params | [BlobQuorumParam](#disperser-BlobQuorumParam) | repeated | 此数据块参与的仲裁组的参数。 |

<a name="disperser-BlobInfo"></a>

### BlobInfo
BlobInfo包含确认数据块与EigenDA合约所需的信息

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| blob_header | [BlobHeader](#disperser-BlobHeader) |  |  |
| blob_verification_proof | [B


