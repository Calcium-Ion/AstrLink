#!/usr/bin/env python3
"""Generate the <1 MiB synthetic sequence-classification ONNX fixture."""

from pathlib import Path

import numpy as np
import onnx
from onnx import TensorProto, helper, numpy_helper


def main() -> None:
    input_ids = helper.make_tensor_value_info(
        "input_ids", TensorProto.INT64, ["batch", "sequence"]
    )
    attention_mask = helper.make_tensor_value_info(
        "attention_mask", TensorProto.INT64, ["batch", "sequence"]
    )
    logits = helper.make_tensor_value_info("logits", TensorProto.FLOAT, ["batch", 4])

    cast = helper.make_node("Cast", ["attention_mask"], ["mask_f"], to=TensorProto.FLOAT)
    axes = helper.make_node(
        "Constant",
        [],
        ["axes"],
        value=numpy_helper.from_array(np.array([1], dtype=np.int64)),
    )
    reduce_sum = helper.make_node(
        "ReduceSum",
        ["mask_f", "axes"],
        ["mask_sum"],
        keepdims=1,
    )
    weights = helper.make_node(
        "Constant",
        [],
        ["weights"],
        value=numpy_helper.from_array(
            np.array([[0.25, 0.0, 0.0, 0.0]], dtype=np.float32)
        ),
    )
    matmul = helper.make_node("MatMul", ["mask_sum", "weights"], ["logits"])

    graph = helper.make_graph(
        [cast, axes, reduce_sum, weights, matmul],
        "astrlink_synthetic_classifier",
        [input_ids, attention_mask],
        [logits],
    )
    model = helper.make_model(
        graph,
        producer_name="astrlink-classifier-worker",
        opset_imports=[helper.make_opsetid("", 18)],
    )
    model.ir_version = 10
    onnx.checker.check_model(model)
    destination = Path(__file__).resolve().parents[1] / "testdata" / "synthetic-classifier.onnx"
    destination.parent.mkdir(parents=True, exist_ok=True)
    onnx.save(model, destination)
    print(f"wrote {destination} ({destination.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
