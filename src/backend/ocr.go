package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OCRProvider 是可插拔的图像识别适配器。
// 后端只依赖「图片字节 → 文本行」这一个契约，将来换成 Tesseract、云 OCR
// 或多模态模型，都不需要改解析与业务代码。
type OCRProvider struct {
	Python    string
	Script    string
	Available bool
	Engine    string
	Hint      string
}

type ocrPythonResponse struct {
	OK     bool      `json:"ok"`
	Error  string    `json:"error"`
	Engine string    `json:"engine"`
	Lines  []OCRLine `json:"lines"`
	Text   string    `json:"text"`
}

func NewOCRProvider(root, python string) *OCRProvider {
	if python == "" {
		python = "python"
	}
	return &OCRProvider{
		Python: python,
		Script: filepath.Join(root, "scripts", "ocr.py"),
		Hint:   "OCR 引擎未就绪",
	}
}

func (p *OCRProvider) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.Python, append([]string{p.Script}, args...)...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errOut.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("%s", detail)
	}
	// 第三方库可能往 stdout 打日志，只取最后一行 JSON
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) == 0 {
		return nil, fmt.Errorf("OCR 脚本没有输出")
	}
	return bytes.TrimSpace(lines[len(lines)-1]), nil
}

func (p *OCRProvider) Check() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	raw, err := p.run(ctx, nil, "--check")
	if err != nil {
		p.Available = false
		p.Hint = fmt.Sprintf("OCR 依赖未就绪：%s。可执行 python -m pip install rapidocr_onnxruntime，或使用页面中的「粘贴文本解析」兜底。", err)
		return
	}
	var r ocrPythonResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		p.Available = false
		p.Hint = "OCR 自检返回格式异常，请使用「粘贴文本解析」兜底。"
		return
	}
	if !r.OK {
		p.Available = false
		p.Hint = r.Error + "。可执行 python -m pip install rapidocr_onnxruntime，或使用「粘贴文本解析」兜底。"
		return
	}
	p.Available = true
	p.Engine = r.Engine
	p.Hint = ""
}

func (p *OCRProvider) Recognize(data []byte) ([]OCRLine, string, error) {
	if !p.Available {
		return nil, "", fmt.Errorf("OCR 引擎不可用：%s", p.Hint)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	raw, err := p.run(ctx, data)
	if err != nil {
		return nil, "", fmt.Errorf("识别失败：%s", err)
	}
	var r ocrPythonResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, "", fmt.Errorf("识别结果格式异常：%v", err)
	}
	if !r.OK {
		return nil, "", fmt.Errorf("识别失败：%s", r.Error)
	}
	if len(r.Lines) == 0 {
		return nil, "", fmt.Errorf("图片中没有识别到文字，请换一张更清晰的截图，或用「粘贴文本解析」")
	}
	return r.Lines, r.Engine, nil
}
