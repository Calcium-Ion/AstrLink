"""Micro-model tests; no production weights or downloads are required."""

import unittest

import numpy as np
import onnx
import onnxruntime as ort
from onnxruntime.quantization.matmul_nbits_quantizer import (
    DefaultWeightOnlyQuantConfig,
    MatMulNBitsQuantizer,
)

from pplx_int4_correction import unpack_matmul_weights


class WeightUnpackingTest(unittest.TestCase):
    def test_unpacked_weights_match_native_kernel_including_padding_and_signs(self):
        random = np.random.default_rng(2026)
        weight = random.normal(size=(48, 16)).astype(np.float32)
        inputs = random.normal(size=(3, 48)).astype(np.float32)
        for block_size in (16, 32, 64):
            with self.subTest(block_size=block_size):
                graph = onnx.helper.make_graph(
                    [
                        onnx.helper.make_node(
                            "MatMul", ["x", "weight"], ["y"], name="projection"
                        )
                    ],
                    "synthetic-quantization-kernel",
                    [
                        onnx.helper.make_tensor_value_info(
                            "x", onnx.TensorProto.FLOAT, [None, 48]
                        )
                    ],
                    [
                        onnx.helper.make_tensor_value_info(
                            "y", onnx.TensorProto.FLOAT, [None, 16]
                        )
                    ],
                    [onnx.numpy_helper.from_array(weight, "weight")],
                )
                model = onnx.helper.make_model(
                    graph,
                    ir_version=10,
                    opset_imports=[onnx.helper.make_opsetid("", 21)],
                )
                quantizer = MatMulNBitsQuantizer(
                    model,
                    algo_config=DefaultWeightOnlyQuantConfig(
                        block_size=block_size,
                        is_symmetric=True,
                        accuracy_level=1,
                    ),
                )
                quantizer.process()
                model = quantizer.model.model
                unpacked = unpack_matmul_weights(
                    model.graph.node[0], {w.name: w for w in model.graph.initializer}
                )
                options = ort.SessionOptions()
                options.intra_op_num_threads = 1
                session = ort.InferenceSession(
                    model.SerializeToString(),
                    options,
                    providers=["CPUExecutionProvider"],
                )
                actual = session.run(None, {"x": inputs})[0]
                np.testing.assert_allclose(
                    actual, inputs @ unpacked, rtol=2e-5, atol=2e-5
                )


if __name__ == "__main__":
    unittest.main()
