#!/usr/bin/env bash
set -euo pipefail

out=${1:?runtime output directory required}
mkdir -p "$out"
out=$(realpath "$out")
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
revision=4979e04f5dcaccb36057e059bbaed8a2f5288315
curl -fsSL --retry 3 "https://codeload.github.com/ggml-org/whisper.cpp/tar.gz/$revision" -o "$work/source.tar.gz"
tar -xzf "$work/source.tar.gz" -C "$work"
source="$work/whisper.cpp-$revision"
for backend in ${MYGO_WHISPER_BACKENDS:-cpu cpu-avx2 vulkan}; do
    vulkan=OFF
    vector=OFF
    if [[ "$backend" == cpu-avx2 ]]; then
        vector=ON
    elif [[ "$backend" != cpu && "$backend" != vulkan ]]; then
        echo "Unknown Whisper backend: $backend" >&2
        exit 1
    fi
    jobs=${MYGO_WHISPER_JOBS:-2}
    if [[ "$backend" == vulkan ]]; then
        vulkan=ON
        jobs=1
    fi
    if [[ "$backend" == vulkan ]]; then
        python3 - "$source/examples/cli/cli.cpp" <<'PYTHON'
from pathlib import Path
import sys

path = Path(sys.argv[1])
text = path.read_text()
anchor = "    ggml_backend_load_all();"
assert text.count(anchor) == 1
probe = """
    if (argc == 2 && std::string(argv[1]) == "--mygo-probe-vulkan") {
        for (size_t i = 0; i < ggml_backend_dev_count(); ++i) {
            auto device = ggml_backend_dev_get(i);
            if (std::string(ggml_backend_dev_name(device)).rfind("Vulkan", 0) != 0) {
                continue;
            }
            if (auto backend = ggml_backend_dev_init(device, nullptr)) {
                ggml_backend_free(backend);
                return 0;
            }
        }
        return 2;
    }
"""
path.write_text(text.replace(anchor, anchor + probe))
PYTHON
    fi
    cmake -S "$source" -B "$work/$backend" -G Ninja \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_C_FLAGS_RELEASE="-O2 -DNDEBUG -ffile-prefix-map=$work=." \
        -DCMAKE_CXX_FLAGS_RELEASE="-O2 -DNDEBUG -ffile-prefix-map=$work=." -DBUILD_SHARED_LIBS=OFF \
        -DCMAKE_EXE_LINKER_FLAGS="-static-libgcc -static-libstdc++" \
        -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF -DGGML_VULKAN="$vulkan" \
        -DGGML_SSE42="$vector" -DGGML_AVX="$vector" -DGGML_AVX2="$vector" \
        -DGGML_BMI2="$vector" -DGGML_FMA="$vector" -DGGML_F16C="$vector" \
        -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF
    cmake --build "$work/$backend" --target whisper-cli --parallel "$jobs"
    install -m 755 "$work/$backend/bin/whisper-cli" "$out/whisper-cli-$backend"
    strip "$out/whisper-cli-$backend"
done
install -m 644 "$source/LICENSE" "$out/LICENSE"
