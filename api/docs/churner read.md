这个文件的主要内容总结如下：
这是一个名为"churner.proto"的协议缓冲区(Protocol Buffer)定义文件。
它定义了一个名为"Churner"的服务，用于处理新运营商加入EigenDA网络的请求。 
文件中定义了几个主要的消息类型：ChurnRequest、ChurnReply、OperatorToChurn和SignatureWithSaltAndExpiry。
Churner服务有一个名为"Churn"的RPC方法，接受ChurnRequest并返回ChurnReply。
文件详细描述了每个消息类型的字段及其用途。
文件还包含了一些关于EigenDA网络运营商管理的背景信息。






# 协议文档
<a name="top"></a>

## 目录

- [churner.proto](#churner-proto)
  - [ChurnReply](#churner-ChurnReply)
  - [ChurnRequest](#churner-ChurnRequest)
  - [OperatorToChurn](#churner-OperatorToChurn)
  - [SignatureWithSaltAndExpiry](#churner-SignatureWithSaltAndExpiry)

  - [Churner](#churner-Churner)

- [标量值类型](#scalar-value-types)

<a name="churner-proto"></a>
<p align="right"><a href="#top">顶部</a></p>

## churner.proto

<a name="churner-ChurnReply"></a>

### ChurnReply

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| signature_with_salt_and_expiry | [SignatureWithSaltAndExpiry](#churner-SignatureWithSaltAndExpiry) |  | Churner签名的签名。 |
| operators_to_churn | [OperatorToChurn](#churner-OperatorToChurn) | repeated | 被淘汰的现有运营商列表。此列表将包含ChurnRequest中指定的所有仲裁组，即使某些仲裁组可能没有被淘汰的运营商。如果仲裁组有可用空间，OperatorToChurn对象将包含仲裁组ID和空的运营商和公钥。智能合约应仅淘汰已满仲裁组的运营商。

例如，如果ChurnRequest指定仲裁组0和1，其中仲裁组0已满而仲裁组1有可用空间，ChurnReply将包含两个具有相应仲裁组的OperatorToChurn对象。仲裁组0的OperatorToChurn将包含要淘汰的运营商，而仲裁组1的OperatorToChurn将包含空运营商（零地址）和公钥。智能合约应仅淘汰仲裁组0的运营商，因为仲裁组1有可用空间而无需淘汰任何运营商。注意：运营商可能仅被淘汰出一个或多个仲裁组（而不是完全从所有仲裁组中淘汰）。 |

<a name="churner-ChurnRequest"></a>

### ChurnRequest

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| operator_address | [string](#string) |  | 运营商的以太坊地址（十六进制格式，如"0x123abcdef..."）。 |
| operator_to_register_pubkey_g1 | [bytes](#bytes) |  | 发出淘汰请求的运营商。 |
| operator_to_register_pubkey_g2 | [bytes](#bytes) |  |  |
| operator_request_signature | [bytes](#bytes) |  | 运营商在keccak256哈希值concat("ChurnRequest", operator address, g1, g2, salt)上的BLS签名。 |
| salt | [bytes](#bytes) |  | 用作operator_request_signature签名消息一部分的盐值。 |
| quorum_ids | [uint32](#uint32) | repeated | 要注册的仲裁组。注意：- 如果这里的任何仲裁组已经注册，整个请求将无法继续。- 如果任何仲裁组注册失败，整个请求将失败。- 无论指定的仲裁组是否已满，Churner都将返回所有指定仲裁组的参数。智能合约将根据仲裁组是否有可用空间来决定是否需要淘汰现有运营商。ID必须在[0, 254]范围内。 |

<a name="churner-OperatorToChurn"></a>

### OperatorToChurn
这描述了要从仲裁组中淘汰的运营商。

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| quorum_id | [uint32](#uint32) |  | 要淘汰的运营商所在仲裁组的ID。 |
| operator | [bytes](#bytes) |  | 运营商的地址。 |
| pubkey | [bytes](#bytes) |  | 运营商的BLS公钥（G1点）。 |

<a name="churner-SignatureWithSaltAndExpiry"></a>

### SignatureWithSaltAndExpiry

| 字段 | 类型 | 标签 | 描述 |
| ----- | ---- | ----- | ----------- |
| signature | [bytes](#bytes) |  | Churner对运营商属性的签名。 |
| salt | [bytes](#bytes) |  | 盐值是keccak256哈希值concat("churn", time.Now(), operatorToChurn的OperatorID, Churner的ECDSA私钥) |
| expiry | [int64](#int64) |  | 此淘汰决定的过期时间。 |

<a name="churner-Churner"></a>

### Churner
Churner是一个处理新运营商尝试加入EigenDA网络的淘汰请求的服务。
当EigenDA网络达到最大运营商数量时，任何尝试加入的新运营商都必须向这个Churner发出淘汰请求，
Churner作为唯一的决策者来决定这个新运营商是否可以加入，如果可以，哪个现有运营商将被淘汰
（以便不超过最大运营商数量）。
最大运营商数量以及制定淘汰决策的规则在链上定义，详见OperatorSetParam：
https://github.com/Layr-Labs/eigenlayer-middleware/blob/master/src/interfaces/IBLSRegistryCoordinatorWithIndices.sol#L24。

| 方法名 | 请求类型 | 响应类型 | 描述 |
| ----------- | ------------ | ------------- | ------------|
| Churn | [ChurnRequest](#churner-ChurnRequest) | [ChurnReply](#churner-ChurnReply) |  |

## 标量值类型

[此处省略标量值类型表格，内容与原文相同]



