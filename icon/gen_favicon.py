import os
from PIL import Image

SRC = r"D:\tools\WorkBuddy\ai-gateway_v5\icon\ai-gateway-icon.png"
STATIC = r"D:\tools\WorkBuddy\ai-gateway_v5\static"
os.makedirs(STATIC, exist_ok=True)

img = Image.open(SRC).convert("RGBA")
print("source size:", img.size)

# Brand gradient colors (blue -> purple)
GRAD_A = (59, 130, 246)    # #3B82F6
GRAD_B = (139, 92, 246)    # #8B5CF6


def is_light(r, g, b, a):
    """White/light glyph (arch + spark)."""
    return a > 200 and min(r, g, b) > 160 and (max(r, g, b) - min(r, g, b)) < 90


def is_dark(r, g, b):
    """Black/dark fake-transparency corners."""
    return max(r, g, b) < 45


def gradient_canvas(size):
    """Diagonal blue -> purple RGBA canvas."""
    base = Image.new("RGBA", (size, size), GRAD_A + (255,))
    top = Image.new("RGBA", (size, size), GRAD_B + (255,))
    mask = Image.new("L", (size, size))
    mp = mask.load()
    for y in range(size):
        for x in range(size):
            mp[x, y] = int((x + y) / (2 * (size - 1)) * 255)
    return Image.composite(top, base, mask)


def transparent_favicon(size):
    """Tab favicon: white glyph on transparent background."""
    src = img.resize((size, size), Image.LANCZOS).convert("RGBA")
    out = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    sp = src.load()
    op = out.load()
    for y in range(size):
        for x in range(size):
            r, g, b, a = sp[x, y]
            if is_light(r, g, b, a):
                op[x, y] = (255, 255, 255, 255)
    return out


def full_bleed(size):
    """Installed PWA icon: solid brand gradient, no dark corners."""
    src = img.resize((size, size), Image.LANCZOS).convert("RGBA")
    canvas = gradient_canvas(size)
    sp = src.load()
    cp = canvas.load()
    for y in range(size):
        for x in range(size):
            r, g, b, a = sp[x, y]
            if is_light(r, g, b, a) or is_dark(r, g, b):
                # leave gradient (transparent/white zones become brand gradient)
                continue
            cp[x, y] = sp[x, y]
    return canvas


# --- Tab favicons: transparent background (rounded glyph) ---
favicon_ico = transparent_favicon(256)
favicon_ico_path = os.path.join(STATIC, "favicon.ico")
# ICO container: 16,32,48,64,128,256
ico_sizes = [16, 32, 48, 64, 128, 256]
frames = [transparent_favicon(s) for s in ico_sizes]
frames[0].save(
    favicon_ico_path,
    format="ICO",
    sizes=[(s, s) for s in ico_sizes],
    append_images=frames[1:],
)
print("wrote", favicon_ico_path)

for name, sz in (("favicon-16x16.png", 16), ("favicon-32x32.png", 32)):
    p = os.path.join(STATIC, name)
    transparent_favicon(sz).save(p, format="PNG")
    print("wrote", p, sz)


# --- Installed PWA icons: full-bleed opaque gradient tiles ---
for name, sz in (
    ("apple-touch-icon.png", 180),
    ("android-chrome-192x192.png", 192),
    ("android-chrome-512x512.png", 512),
):
    p = os.path.join(STATIC, name)
    full_bleed(sz).save(p, format="PNG")
    print("wrote", p, sz)

print("done")
