"""本地离线 OCR sidecar。

协议：stdin 收图片原始字节 → stdout 输出一行 JSON。
Go 后端通过 exec 调用本脚本，识别引擎可整体替换，不影响后端架构。

依赖：python -m pip install rapidocr_onnxruntime
（自带中英文模型，纯本地推理，无需联网、无需密钥）
"""
import io
import json
import sys

_real_stdout = sys.stdout
sys.stdout = sys.stderr  # 第三方库的日志一律走 stderr，保证 stdout 只有 JSON


def emit(obj):
    _real_stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    _real_stdout.flush()


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "--check":
        try:
            import rapidocr_onnxruntime  # noqa: F401
            import numpy  # noqa: F401
            from PIL import Image  # noqa: F401
            emit({"ok": True, "engine": "RapidOCR(onnxruntime)"})
        except Exception as exc:
            emit({"ok": False, "error": "OCR 依赖未就绪: %s" % exc})
        return

    data = sys.stdin.buffer.read()
    if not data:
        emit({"ok": False, "error": "没有收到图片数据"})
        return

    try:
        import numpy as np
        from PIL import Image
        from rapidocr_onnxruntime import RapidOCR

        img = Image.open(io.BytesIO(data)).convert("RGB")
        arr = np.array(img)
        engine = RapidOCR()
        result, _ = engine(arr)
        lines = []
        if result:
            for box, text, score in result:
                lines.append({
                    "text": str(text),
                    "score": float(score),
                    "box": [[float(x), float(y)] for x, y in box],
                })
        emit({
            "ok": True,
            "engine": "RapidOCR(onnxruntime)",
            "lines": lines,
            "text": "\n".join(line["text"] for line in lines),
        })
    except Exception as exc:
        emit({"ok": False, "error": str(exc)})


if __name__ == "__main__":
    main()
