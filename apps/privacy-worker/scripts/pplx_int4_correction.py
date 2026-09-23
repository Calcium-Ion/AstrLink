"""Synthetic calibration and activation-weighted low-rank INT4 error repair.

Calibration text is independent of the labelled regression/holdout fixtures.
No customer requests, source repositories, or network services are accessed.
"""

from collections import OrderedDict
import hashlib
import json
import os
import shutil

import numpy as np
import onnx
import onnxruntime as ort
from tokenizers import Tokenizer
import torch


def calibration_texts():
    texts = []
    for i in range(24):
        examples = [
            f"function fetchPage(offset: number): Promise<Response> {{ return client.get(`/items?offset=${{offset}}`); }}\nconst attempts = {i + 2};\n",
            f"订单记录：联系人林晓敏，联系电话13987654321，邮箱为 contact{i}@willow-support.net。服务地址为上海市浦东新区银城路{30 + i}号。\n",
            'from pathlib import Path\nimport json\nclass Settings:\n    def __init__(self, environ):\n        self.endpoint = environ.get("SERVICE_ENDPOINT")\n        self.credential = environ.get("SERVICE_CREDENTIAL")\n',
            f'{{"customer":"Sophie Bennett","email":"sophie{i}@willow-customer.net","mobile":"+44 7700 900123","dob":"1991-04-23"}}\n',
            "fn checked_sum(values: &[u32]) -> Option<u32> { values.iter().try_fold(0_u32, |acc, value| acc.checked_add(*value)) }\n",
            'DB_URI="postgresql://app:K8w!R3p#N6t@db.internal:5432/inventory"\nAuthorization: Bearer sk-live-7mT9vW2xY4zA6bC8dE0fG3hJ5kL7nP9qR\n',
            "if err != nil { return nil, errors.Join(ErrUnavailable, err) }\nfor index, row := range rows { cache[index] = transform(row) }\n",
            f"The customer asks to remove the account number 938475610283 and their postal address 219 Willow Lane, Portland, OR 97205 from the export. Tracking sequence: {i}.\n",
            "export interface Profile { givenName?: string; mail: string | null; credentialHash: Uint8Array }\nconst profile = await repository.lookup(identifier);\n",
            f"CREATE INDEX IF NOT EXISTS idx_entries_created ON entries(created_at);\nSELECT category, COUNT(*) FROM entries WHERE state = {i % 4} GROUP BY category;\n",
            "Please explain why the retry queue can starve lower priority requests. The timeout is 120 seconds and the batch limit is 512. Show a safe cancellation path with unit tests.\n",
            "帮我检查这段解析代码。空数组应该返回空结果，不能访问越界。日志只打印状态码，不输出密码和访问令牌。请保留现有函数签名，并修复并发退出时的错误。\n",
        ]
        texts.append("".join(examples[(i + j) % len(examples)] for j in range(5)))
    return texts


def capture_activations(source, directory, excluded):
    """Run the FP32 model once per sample and spool projections to local files."""
    directory.mkdir()
    model = onnx.load(source / "model.onnx", load_external_data=False)
    weights = {weight.name: weight for weight in model.graph.initializer}
    inputs = OrderedDict()
    for node in model.graph.node:
        if (
            node.op_type == "MatMul"
            and node.name not in excluded
            and node.input[1] in weights
        ):
            inputs[node.input[0]] = weights[node.input[1]].dims[0]
    for name, columns in inputs.items():
        model.graph.output.append(
            onnx.helper.make_tensor_value_info(
                name, onnx.TensorProto.FLOAT, [None, None, columns]
            )
        )
    onnx.save_model(model, directory / "calibration.onnx")
    # Avoid copying 2.4 GB for an offline graph whose external data is unchanged.
    try:
        os.link(source / "model.onnx.data", directory / "model.onnx.data")
    except OSError:
        shutil.copyfile(source / "model.onnx.data", directory / "model.onnx.data")
    options = ort.SessionOptions()
    options.intra_op_num_threads = 4
    options.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_BASIC
    session = ort.InferenceSession(
        str(directory / "calibration.onnx"), options, providers=["CPUExecutionProvider"]
    )
    tokenizer = Tokenizer.from_file(str(source / "tokenizer.json"))
    tokenizer.no_truncation()
    tokenizer.no_padding()
    texts = calibration_texts()
    entries = {
        name: {"path": directory / f"activation-{index}.bin", "columns": columns}
        for index, (name, columns) in enumerate(inputs.items())
    }
    handles = {name: entry["path"].open("wb") for name, entry in entries.items()}
    rows = 0
    try:
        for text in texts:
            ids = np.array([tokenizer.encode(text).ids[:128]], dtype=np.int64)
            outputs = session.run(
                list(inputs), {"input_ids": ids, "attention_mask": np.ones_like(ids)}
            )
            rows += ids.shape[1]
            for name, value in zip(inputs, outputs, strict=True):
                handles[name].write(value.astype(np.float32).tobytes())
    finally:
        for handle in handles.values():
            handle.close()
    return (
        entries,
        rows,
        {
            "corpus": "synthetic-code-and-privacy-v1",
            "sha256": hashlib.sha256(
                json.dumps(texts, ensure_ascii=False).encode()
            ).hexdigest(),
            "samples": len(texts),
            "tokens": rows,
            "max_sample_tokens": 128,
        },
    )


def unpack_matmul_weights(node, weights):
    """Decode symmetric ONNX MatMulNBits weights for error estimation only."""
    attributes = {
        attr.name: onnx.helper.get_attribute_value(attr) for attr in node.attribute
    }
    if attributes["bits"] != 4 or len(node.input) != 3:
        raise ValueError("Expected symmetric four-bit MatMulNBits without zero points")
    packed = onnx.numpy_helper.to_array(weights[node.input[1]])
    scales = onnx.numpy_helper.to_array(weights[node.input[2]])
    codes = np.empty((*packed.shape[:-1], packed.shape[-1] * 2), dtype=np.float32)
    codes[..., 0::2] = packed & 15
    codes[..., 1::2] = packed >> 4
    expanded = (codes - 8) * scales.reshape(*packed.shape[:-1], 1)
    return expanded.reshape(attributes["N"], -1)[:, : attributes["K"]].T


def add_corrections(model, source, directory, excluded, rank=64):
    entries, rows, calibration = capture_activations(source, directory, excluded)
    original = onnx.load(source / "model.onnx", load_external_data=False)
    original_weights = {weight.name: weight for weight in original.graph.initializer}
    original_nodes = {node.name: node for node in original.graph.node}
    weights = {weight.name: weight for weight in model.graph.initializer}
    nodes = []
    ratios = []
    torch.set_num_threads(4)
    torch.manual_seed(42)
    torch.use_deterministic_algorithms(True)
    for node in model.graph.node:
        if node.op_type != "MatMulNBits":
            nodes.append(node)
            continue
        parent = original_nodes[node.name.removesuffix("_Q4")]
        weight = onnx.numpy_helper.to_array(
            original_weights[parent.input[1]], base_dir=str(source)
        ).copy()
        error = torch.from_numpy(weight - unpack_matmul_weights(node, weights))
        entry = entries[parent.input[0]]
        activation = np.memmap(
            entry["path"], dtype="float32", mode="r", shape=(rows, entry["columns"])
        )
        residual = torch.from_numpy(activation.copy()) @ error
        # Choose the output directions where quantization most affects actual
        # activations. Only the small factors are shipped, never calibration text.
        _, _, right = torch.svd_lowrank(residual, q=rank + 8, niter=2)
        up = right[:, :rank].T.contiguous()
        down = (error @ up.T).contiguous()
        ratios.append(
            float(
                torch.linalg.vector_norm(residual - residual @ up.T @ up)
                / torch.linalg.vector_norm(residual)
            )
        )
        prefix = node.name + "_correction"
        output = node.output[0]
        node.output[0] = prefix + "_base"
        nodes.extend(
            [
                node,
                onnx.helper.make_node(
                    "Cast",
                    [prefix + "_a_f16"],
                    [prefix + "_a"],
                    name=prefix + "_cast_a",
                    to=onnx.TensorProto.FLOAT,
                ),
                onnx.helper.make_node(
                    "Cast",
                    [prefix + "_b_f16"],
                    [prefix + "_b"],
                    name=prefix + "_cast_b",
                    to=onnx.TensorProto.FLOAT,
                ),
                onnx.helper.make_node(
                    "MatMul",
                    [node.input[0], prefix + "_a"],
                    [prefix + "_low"],
                    name=prefix + "_down",
                ),
                onnx.helper.make_node(
                    "MatMul",
                    [prefix + "_low", prefix + "_b"],
                    [prefix + "_delta"],
                    name=prefix + "_up",
                ),
                onnx.helper.make_node(
                    "Add",
                    [prefix + "_base", prefix + "_delta"],
                    [output],
                    name=prefix + "_add",
                ),
            ]
        )
        model.graph.initializer.extend(
            [
                onnx.numpy_helper.from_array(
                    down.numpy().astype(np.float16), prefix + "_a_f16"
                ),
                onnx.numpy_helper.from_array(
                    up.numpy().astype(np.float16), prefix + "_b_f16"
                ),
            ]
        )
        if len(ratios) % 7 == 0:
            print(f"Calibrated layer {len(ratios) // 7}/28", flush=True)
    del model.graph.node[:]
    model.graph.node.extend(nodes)
    return {
        "method": "activation-weighted-output-low-rank-residual",
        "rank": rank,
        "storage_precision": "fp16",
        "compute_precision": "fp32",
        "seed": 42,
        "threads": 4,
        "svd_oversampling": 8,
        "svd_iterations": 2,
        "calibration": calibration,
        "mean_calibration_residual_ratio": sum(ratios) / len(ratios),
    }
