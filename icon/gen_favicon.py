import os
from PIL import Image

SRC = r"D:\tools\WorkBuddy\ai-gateway_v5\icon\ai-gateway-icon.png"
STATIC = r"D:\tools\WorkBuddy\ai-gateway_v5\static"
os.makedirs(STATIC, exist_ok=True)

img = Image.open(SRC).convert("RGBA")
print("source size:", img.size)

# --- favicon.ico: multi-resolution container ---
ico_sizes = [16, 32, 48, 64, 128, 256]
ico_frames = [img.resize((s, s), Image.LANCZOS) for s in ico_sizes]
ico_path = os.path.join(STATIC, "favicon.ico")
ico_frames[0].save(
    ico_path,
    format="ICO",
    sizes=[(s, s) for s in ico_sizes],
    append_images=ico_frames[1:],
)
print("wrote", ico_path)

# --- standard PNG favicon set ---
png_set = {
    "favicon-16x16.png": 16,
    "favicon-32x32.png": 32,
    "apple-touch-icon.png": 180,
    "android-chrome-192x192.png": 192,
    "android-chrome-512x512.png": 512,
}
for name, sz in png_set.items():
    out = img.resize((sz, sz), Image.LANCZOS)
    p = os.path.join(STATIC, name)
    out.save(p, format="PNG")
    print("wrote", p, sz)

print("done")
