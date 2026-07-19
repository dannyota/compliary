# compliary batch embed kernel — runs on a Kaggle GPU session.
#
# Reads input.jsonl (one {"index": i, "text": "..."} per line) mounted from the
# compliary-embed-input Kaggle dataset, embeds every text with Qwen3-Embedding-0.6B
# (ONNX FP16, onnxruntime), and writes /kaggle/working/vectors.jsonl.gz (gzip;
# one {"index": i, "embedding": [..1024..]} JSON line per row).
#
# The embedding recipe MUST match compliary's Go ONNX embedder exactly:
# Qwen3-Embedding dense = last-token pooling + L2 normalize, 1024-d.
# Documents are embedded WITHOUT an instruction prefix (asymmetric model —
# only queries get "Instruct: ...\nQuery:...").
#
# The model loads offline from a mounted Kaggle dataset mirror
# (danhsoftware/qwen3-embedding-06b-onnx-fp16 containing model_fp16.onnx +
# model_fp16.onnx_data + tokenizer.json). Internet is enabled for
# pip-installing onnxruntime-gpu and tokenizers (not pre-installed on Kaggle
# GPU images). Pin to a version compatible with Kaggle's CUDA 12.x — ORT
# 1.27.0 GPU requires CUDA 13.

import subprocess, sys

# Kaggle captures stdout through a pipe (not a TTY), so Python block-buffers
# prints and progress lines surface minutes late or only at process exit.
# Line-buffer both streams so every print lands in the kernel log immediately.
sys.stdout.reconfigure(line_buffering=True)
sys.stderr.reconfigure(line_buffering=True)

subprocess.check_call([sys.executable, "-m", "pip", "install", "-q",
                       "onnxruntime-gpu==1.26.0", "tokenizers"])

import glob
import gzip
import json
import os
import threading
import traceback

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer

INPUT_ROOT = "/kaggle/input"
OUTPUT_PATH = "/kaggle/working/vectors.jsonl.gz"
MODEL_FILENAME = "model_fp16.onnx"
TOKENIZER_FILENAME = "tokenizer.json"
MAX_LENGTH = 8192
# Memory budget for CUTLASS memory-efficient attention (sm_75 T4).
PAD_STEP = 128                 # pads quantized to multiples of this
TOKEN_BUDGET = 128 * 1024      # 128k tokens max count*pad per sess.run (KV+mask combined)
ATTN_MASK_BUDGET = 50_000_000  # count*pad^2 cap — keeps mask tile under ~1.6 GB (8 heads * 4B)
DIMS = 1024


def round_pad(length):
    """Quantize a token length up to the next PAD_STEP multiple, capped at MAX_LENGTH."""
    return min(((max(length, 1) + PAD_STEP - 1) // PAD_STEP) * PAD_STEP, MAX_LENGTH)


def count_for(pad, n_gpus=1):
    """Deterministic row count for a pad: largest count under the per-GPU KV
    budget, floored at 1 so an outlier near MAX_LENGTH still forms a batch."""
    per_gpu = TOKEN_BUDGET // n_gpus
    kv_count = per_gpu // pad
    mask_count = ATTN_MASK_BUDGET // (pad * pad)
    return max(1, min(kv_count, mask_count))


def find_input():
    """Locate the input JSONL under /kaggle/input."""
    preferred = glob.glob(f"{INPUT_ROOT}/**/compliary-embed-*/**/input.jsonl", recursive=True)
    if preferred:
        return preferred[0]
    any_input = glob.glob(f"{INPUT_ROOT}/**/input.jsonl", recursive=True)
    if any_input:
        return any_input[0]
    return None


def find_model_dir():
    """Find the mounted ONNX model directory containing model_fp16.onnx + tokenizer.json."""
    for model_path in glob.glob(f"{INPUT_ROOT}/**/{MODEL_FILENAME}", recursive=True):
        d = os.path.dirname(model_path)
        if os.path.exists(os.path.join(d, TOKENIZER_FILENAME)):
            return d
    return None


def load_tokenizer(model_dir):
    tok = Tokenizer.from_file(os.path.join(model_dir, TOKENIZER_FILENAME))
    tok.enable_truncation(max_length=MAX_LENGTH)
    return tok


def gpu_count():
    """Number of visible NVIDIA GPUs."""
    try:
        out = subprocess.run(["nvidia-smi", "-L"], capture_output=True, text=True, timeout=30)
        n = len([ln for ln in out.stdout.splitlines() if ln.strip().startswith("GPU ")])
        return max(1, n)
    except Exception:
        return 1


def load_session(model_dir, device_id=0):
    model_path = os.path.join(model_dir, MODEL_FILENAME)

    os.environ.pop("ORT_DISABLE_MEMORY_EFFICIENT_ATTENTION", None)
    os.environ["ORT_DISABLE_FUSED_ATTENTION"] = "0"
    os.environ["ORT_DISABLE_TRT_FLASH_ATTENTION"] = "1"
    os.environ["ORT_DISABLE_FUSED_CROSS_ATTENTION"] = "1"

    available = ort.get_available_providers()
    if "CUDAExecutionProvider" not in available:
        raise RuntimeError(
            "CUDAExecutionProvider not available — onnxruntime-gpu not installed "
            "or no GPU visible. The Kaggle kernel must request a GPU accelerator "
            f"(e.g. NvidiaTeslaT4). Available providers: {available}")

    providers = [("CUDAExecutionProvider", {
        "device_id": device_id,
        "arena_extend_strategy": "kSameAsRequested",
    }), "CPUExecutionProvider"]
    sess = ort.InferenceSession(model_path, providers=providers)
    active = sess.get_providers()
    print(f"ORT providers (gpu {device_id}):", active)

    if "CUDAExecutionProvider" not in active:
        raise RuntimeError(
            "Session fell back to CPU — CUDAExecutionProvider failed to "
            f"initialize. Active providers: {active}")

    input_names = [inp.name for inp in sess.get_inputs()]
    output_names = [out.name for out in sess.get_outputs()]
    print("inputs:", input_names)
    print("outputs:", output_names[:3], f"... ({len(output_names)} total)" if len(output_names) > 3 else "")
    return sess, input_names, output_names


def build_feeds(input_names, input_ids, attention_mask):
    """Build the ORT feed dict, including position_ids and empty KV cache if needed."""
    batch_size, seq_len = input_ids.shape
    feeds = {}
    for name in input_names:
        if name == "input_ids":
            feeds[name] = input_ids
        elif name == "attention_mask":
            feeds[name] = attention_mask
        elif name == "position_ids":
            feeds[name] = np.tile(np.arange(seq_len, dtype=np.int64), (batch_size, 1))
        elif name.startswith("past_key_values"):
            sess_input = next(i for i in sess_inputs_global if i.name == name)
            shape = []
            for dim in sess_input.shape:
                if isinstance(dim, int):
                    shape.append(dim if dim > 0 else 0)
                else:
                    shape.append(batch_size if "batch" in str(dim) else 0)
            feeds[name] = np.zeros(shape, dtype=np.float16)
    return feeds


def last_token_pool(hidden_states, attention_mask):
    """Extract the hidden state at the last non-padding position, then L2 normalize."""
    last_indices = attention_mask.sum(axis=1).astype(np.int64) - 1
    batch_size = hidden_states.shape[0]
    vecs = hidden_states[np.arange(batch_size), last_indices]  # [batch, dims]
    norms = np.linalg.norm(vecs, axis=1, keepdims=True)
    norms = np.where(norms == 0, 1.0, norms)
    return vecs / norms


# Global ref for build_feeds to access input metadata.
sess_inputs_global = None


def main():
    input_path = find_input()
    if not input_path:
        print("ERROR: no input.jsonl found under", INPUT_ROOT, file=sys.stderr)
        sys.exit(1)
    print("input file:", input_path)

    rows = [json.loads(line) for line in open(input_path) if line.strip()]
    indices = [int(r["index"]) for r in rows]
    texts = [r["text"] for r in rows]
    print("loaded", len(texts), "texts")

    model_dir = find_model_dir()
    if not model_dir:
        print("ERROR: no ONNX model found. Mount danhsoftware/qwen3-embedding-06b-onnx-fp16", file=sys.stderr)
        sys.exit(1)
    print("model dir:", model_dir)

    tokenizer = load_tokenizer(model_dir)

    n_gpus = gpu_count()
    print("visible GPUs:", n_gpus)
    sessions = []
    input_names = None
    for dev in range(n_gpus):
        sess, input_names, _ = load_session(model_dir, dev)
        sessions.append(sess)
    global sess_inputs_global
    sess_inputs_global = sessions[0].get_inputs()

    token_ids = [e.ids for e in tokenizer.encode_batch(texts)]
    lengths = [len(ids) for ids in token_ids]
    results = [None] * len(texts)

    order = sorted(range(len(texts)), key=lambda i: lengths[i])
    batches = []
    current = []
    for i in order:
        new_pad = round_pad(lengths[i])
        if current and len(current) + 1 > count_for(new_pad, n_gpus):
            batches.append(current)
            current = [i]
        else:
            current.append(i)
    if current:
        batches.append(current)

    batches.reverse()

    total = len(texts)
    done = 0
    done_lock = threading.Lock()
    worker_errs = [None] * len(sessions)

    def run_shard(dev, sess):
        nonlocal done
        run_opts = ort.RunOptions()
        run_opts.add_run_config_entry("memory.enable_memory_arena_shrinkage", f"gpu:{dev}")
        my_batches = [(o, b) for o, b in enumerate(batches) if o % len(sessions) == dev]
        for ordinal, real in my_batches:
            n_real = len(real)
            final_pad = round_pad(max(lengths[i] for i in real))
            final_count = count_for(final_pad, n_gpus)

            actual_count = min(final_count, n_real)
            input_ids = np.full((actual_count, final_pad), 151643, dtype=np.int64)
            attention_mask = np.zeros((actual_count, final_pad), dtype=np.int64)
            for row in range(actual_count):
                ids = token_ids[real[row]]
                input_ids[row, :len(ids)] = ids
                attention_mask[row, :len(ids)] = 1

            feeds = build_feeds(input_names, input_ids, attention_mask)
            try:
                out = sess.run(["last_hidden_state"], feeds, run_opts)
            except Exception as e:
                print(f"  sess.run FAILED at batch {ordinal} gpu={dev} "
                      f"(input_ids shape count={actual_count} pad={final_pad}): {repr(e)}",
                      flush=True)
                traceback.print_exc()
                raise
            hidden_states = out[0]

            vecs = last_token_pool(hidden_states, attention_mask)

            for row, i in enumerate(real):
                results[i] = vecs[row].tolist()

            with done_lock:
                done += n_real
                print(f"  batch {ordinal} gpu={dev}: {done}/{total} embedded "
                      f"({actual_count}x{final_pad}, max_count {final_count})", flush=True)

    def worker(dev, sess):
        try:
            run_shard(dev, sess)
        except Exception as e:  # noqa: BLE001
            worker_errs[dev] = e

    threads = [threading.Thread(target=worker, args=(d, s), daemon=True)
               for d, s in enumerate(sessions)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    for e in worker_errs:
        if e is not None:
            raise e

    dims = len(results[0]) if results else 0
    print("writing", len(results), "vectors, dims", dims)
    with gzip.open(OUTPUT_PATH, "wt") as f:
        for idx, embedding in zip(indices, results):
            f.write(json.dumps({"index": idx, "embedding": embedding}) + "\n")
    print("done:", OUTPUT_PATH)


main()
