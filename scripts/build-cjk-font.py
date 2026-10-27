#!/usr/bin/env python3
"""生成界面用的中文字体子集:internal/ui/assets/fonts/NotoSansSC-subset.woff2。

字符集 = GB2312 一级和二级字(6763 个)
       + 界面资产(internal/ui/assets 下 js/html/css)、Go 源码(界面会显示的
         ui_hint 等)和 CHANGELOG(更新弹窗里的发布说明)里出现的全部中文、全角标点
       + CJK 标点、全角符号两个整块。
同时写出 NotoSansSC-subset.txt(子集里的字,按码位排序;第一行是 woff2 的 sha256),
internal/ui/fonts_test.go 用它检查「界面上的字都在子集里」和「字表与字体文件对得上」。

用法(要 fonttools + brotli):
  python3 scripts/build-cjk-font.py /path/to/NotoSansSC[wght].ttf
源字体:https://github.com/google/fonts/tree/main/ofl/notosanssc(SIL OFL 1.1)。
"""
import hashlib, pathlib, sys
from fontTools import subset
from fontTools.ttLib import TTFont

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "internal/ui/assets/fonts/NotoSansSC-subset.woff2"
LIST = OUT.with_suffix(".txt")

def wide(ch):
    return ord(ch) >= 0x2E80 or 0x2010 <= ord(ch) <= 0x206F  # 中日韩各区 + 全角 + 常用标点(— … “ ” ·)

def main(src):
    chars = set()
    for b1 in range(0xB0, 0xF8):  # GB2312 一级 + 二级:B0A1–D7F9、D8A1–F7FE
        for b2 in range(0xA1, 0xFF):
            try:
                chars.add(bytes([b1, b2]).decode("gb2312"))
            except UnicodeDecodeError:
                pass
    chars.update(chr(c) for c in range(0x3000, 0x3040))
    chars.update(chr(c) for c in range(0xFF01, 0xFF5F))
    files = [*ROOT.glob("internal/ui/assets/*.js"), *ROOT.glob("internal/ui/assets/*.html"), *ROOT.glob("internal/ui/assets/*.css"),
             *ROOT.glob("internal/**/*.go"), *ROOT.glob("cmd/**/*.go"), ROOT / "CHANGELOG.md"]
    for f in files:
        chars.update(c for c in f.read_text(encoding="utf-8") if wide(c))
    font = TTFont(src)
    cmap = font.getBestCmap()
    have = sorted(c for c in chars if ord(c) in cmap)
    opts = subset.Options()
    opts.flavor = "woff2"
    opts.layout_features = ["*"]
    opts.name_IDs = ["*"]
    opts.notdef_outline = True
    opts.drop_tables += ["DSIG"]
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=[ord(c) for c in have])
    sub.subset(font)
    font.flavor = "woff2"
    font.save(OUT)
    digest = hashlib.sha256(OUT.read_bytes()).hexdigest()
    LIST.write_text(digest + "\n" + "".join(have) + "\n", encoding="utf-8")
    print(f"{OUT.relative_to(ROOT)}: {len(have)} 字, {OUT.stat().st_size} 字节, 缺 {len(chars) - len(have)} 个(源字体没有)")

if __name__ == "__main__":
    main(sys.argv[1])
