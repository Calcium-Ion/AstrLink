"""Build AstrLink's PII-Tracer INT4 package from the pinned public FP32 export.

Offline developer tool; Python is not needed by the desktop inference worker.
Install requirements-quantize.txt in an isolated environment, then run:
  python quantize_pplx.py --source /path/to/fp32 --license /path/to/LICENSE \
      --output /path/outside/repository/pii-tracer-int4

The output can be imported through Privacy > Models > Local import. Production
weights must stay outside source control. This tool never downloads or uploads.
"""

import argparse
from collections import Counter
import hashlib
import importlib.metadata
import json
import logging
import os
from pathlib import Path
import shutil
import tempfile

import onnx
from onnxruntime.quantization.matmul_nbits_quantizer import (
    DefaultWeightOnlyQuantConfig,
    MatMulNBitsQuantizer,
)
from onnxruntime.quantization.quant_utils import QuantFormat

from pplx_int4_correction import add_corrections


SOURCE_REPO = "lemonade-sdk/pplx-pii-masking-onnx"
SOURCE_REVISION = "5ba4e413b78ff0f83d3c9cddee1bb5fdccbeee00"
SOURCE_HASHES = {
    "model.onnx": "56309eabe1e3a646718de2b0e32b097dc2a9f3fb9cf54395cac1672a1a93a630",
    "model.onnx.data": "65ac3f46cc6f3f5abeae2ba9bb0b35c7daebb185ff3c420a373bc9bb998a0b5e",
    "config.json": "c57c3d8114ef302c51a35d5eb72a35e02c3e10bd17b8401bc660be593fc46dfc",
    "tokenizer.json": "cae14d1c8dda080f23792355b0692b826bf1f1da3c86ebc1b37548a391cf6526",
    "tokenizer_config.json": "aa9c1b0a1c9b48c2f70bacdf64f7dab25194be4ffea0c6a6e4da262360a91d0a",
}
LICENSE_HASH = "7fbf88e9c951fe53eb614a46772d0b48ada6d50b351e5e11dcb64b4dc3fb8eb2"
VERSIONS = {
    "onnxruntime": "1.23.2",
    "onnx": "1.19.1",
    "numpy": "2.4.6",
    "tokenizers": "0.23.1",
    "torch": "2.8.0",
}


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def source_descriptor():
    """Package metadata for the existing custom-repository installer."""
    return {
        "version": 1,
        "name": "AstrLink PII-Tracer 0.6B INT4",
        "license": "MIT",
        "languages": ["en", "multilingual"],
        "adapter": "pplx_bioes_viterbi",
        "variants": [
            {
                "id": "cpu_int4",
                "name": "CPU INT4",
                "quantization": "int4",
                "estimated_ram_bytes": 2_147_483_648,
                "recommended": True,
                "model_path": "model_int4.onnx",
                "external_data_paths": ["model_int4.onnx.data"],
                "tokenizer_path": "tokenizer.json",
                "config_path": "config.json",
                "tag_scheme": "bioes",
                "window": 1024,
                "stride": 128,
                "max_request_tokens": 131_072,
                "input_names": {
                    "input_ids": "input_ids",
                    "attention_mask": "attention_mask",
                },
                "output_name": "logits",
            }
        ],
    }


def quantize(model, block_size):
    """Quantize backbone weights, preserving both FP32 classifier heads."""
    weights = {weight.name: weight for weight in model.graph.initializer}
    excluded = []
    eligible = Counter()
    for node in model.graph.node:
        if node.op_type not in ("MatMul", "Gather"):
            continue
        index = 1 if node.op_type == "MatMul" else 0
        weight = weights.get(node.input[index])
        if weight is None:
            continue
        # The pinned export names its transposed classifier weight val_5102.
        # Recognize the actual 1024 -> 37 label projection, not a fragile name.
        if node.op_type == "MatMul" and list(weight.dims) == [1024, 37]:
            excluded.append(node.name)
        else:
            eligible[node.op_type] += 1
    if len(excluded) != 1 or eligible != {"MatMul": 196, "Gather": 1}:
        raise ValueError(f"Unexpected PII-Tracer graph: {eligible}, heads={excluded}")
    config = DefaultWeightOnlyQuantConfig(
        block_size=block_size,
        is_symmetric=True,
        # Prefer the CPU INT8 dot-product kernel for INT4 weights. Tensor
        # interfaces stay FP32; the classifier and decoding are unchanged.
        accuracy_level=4,
        quant_format=QuantFormat.QOperator,
        op_types_to_quantize=("MatMul", "Gather"),
        quant_axes=(("MatMul", 0), ("Gather", 1)),
        bits=4,
    )
    quantizer = MatMulNBitsQuantizer(
        model, nodes_to_exclude=excluded, algo_config=config
    )
    quantizer.process()
    result = quantizer.model.model
    counts = Counter(node.op_type for node in result.graph.node)
    if counts["MatMulNBits"] != 196 or counts["GatherBlockQuantized"] != 1:
        raise ValueError("Backbone or embedding quantization was incomplete")
    return result, excluded


def build(source, license_path, output, block_size):
    if os.environ.get("ASTRLINK_CI_SYNTHETIC_MODELS_ONLY") or os.environ.get(
        "ASTRLINK_CI_NO_REMOTE_MODELS"
    ):
        raise ValueError("Production model conversion must run explicitly outside CI")
    if output.exists():
        raise ValueError("Output already exists; use a new directory")
    for package, version in VERSIONS.items():
        if importlib.metadata.version(package) != version:
            raise ValueError(
                f"Install the pinned quantizer dependency: {package}=={version}"
            )
    for name, expected in SOURCE_HASHES.items():
        if sha256(source / name) != expected:
            raise ValueError(f"Source checksum mismatch: {name}")
    if sha256(license_path) != LICENSE_HASH:
        raise ValueError("Expected the original Perplexity MIT license")
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=".pii-int4-", dir=output.parent))
    try:
        model, excluded = quantize(onnx.load(source / "model.onnx"), block_size)
        with tempfile.TemporaryDirectory(
            prefix=".pii-calibration-", dir=output.parent
        ) as calibration_dir:
            correction = add_corrections(
                model, source, Path(calibration_dir) / "activations", excluded
            )
        model_path = staging / "model_int4.onnx"
        onnx.save_model(
            model,
            model_path,
            save_as_external_data=True,
            all_tensors_to_one_file=True,
            location="model_int4.onnx.data",
            size_threshold=1024,
        )
        onnx.checker.check_model(str(model_path))
        for name in ("config.json", "tokenizer.json", "tokenizer_config.json"):
            shutil.copyfile(source / name, staging / name)
        shutil.copyfile(license_path, staging / "LICENSE")
        (staging / "astrlink-model.json").write_text(
            json.dumps(source_descriptor(), indent=2) + "\n",
            encoding="utf-8",
        )
        assets = [
            {"path": path.name, "size": path.stat().st_size, "sha256": sha256(path)}
            for path in sorted(staging.iterdir())
        ]
        provenance = {
            "format_version": 1,
            "name": "AstrLink PII-Tracer 0.6B INT4",
            "base_model": "perplexity-ai/pplx-pii-masking",
            "license": "MIT",
            "source_repo": SOURCE_REPO,
            "source_revision": SOURCE_REVISION,
            "source_sha256": SOURCE_HASHES,
            "tool_versions": VERSIONS,
            "quantization": {
                "bits": 4,
                "symmetric": True,
                "block_size": block_size,
                "algorithm": "RTN",
                "format": "ONNX QOperator",
                "activation_tensors": "fp32",
                "accuracy_level": 4,
                "matmul_compute_preference": "int8",
                "matmul_nodes": 196,
                "embedding_nodes": 1,
                "fp32_token_head_nodes": excluded,
                "sensitivity_head_precision": "fp32",
                "error_correction": correction,
            },
            "bytes_total": sum(asset["size"] for asset in assets),
            "files": assets,
        }
        (staging / "quantization.json").write_text(
            json.dumps(provenance, indent=2) + "\n",
            encoding="utf-8",
        )
        # Publish the directory only after all assets and checksums are complete.
        staging.rename(output)
        return provenance
    finally:
        if staging.exists():
            shutil.rmtree(staging)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--license", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--block-size", type=int, choices=(16, 32, 64, 128), default=32)
    args = parser.parse_args()
    logging.getLogger("onnxruntime.quantization.matmul_nbits_quantizer").setLevel(
        logging.WARNING
    )
    result = build(
        args.source.resolve(),
        args.license.resolve(),
        args.output.resolve(),
        args.block_size,
    )
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
