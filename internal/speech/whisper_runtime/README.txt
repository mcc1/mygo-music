Linux release builds embed whisper.cpp CPU and Vulkan runtimes here.

Build with bash scripts/build-whisper-linux.sh internal/speech/whisper_runtime
on Ubuntu 24.04 with CMake, Ninja, a C/C++ compiler, glslc and libvulkan-dev.
The script pins the upstream source revision, disables optional x86 instruction
sets for the CPU baseline, and embeds CPU, AVX2 CPU and Vulkan executables with
their MIT license. The AVX2 variant runs only when the OS and CPU support AVX,
AVX2, FMA, F16C, BMI2, SSE3 through SSE4.2 and POPCNT; older machines use the
generic CPU executable.
Current binaries require glibc 2.38 or newer. Vulkan also needs libvulkan.so.1
and a working GPU driver; Auto probes the device for up to five seconds before
loading the model, then uses CPU if the probe or transcription fails.

go test ./internal/speech runs the backend selection and cancellation tests.
Set WHISPER_ROOT and WHISPER_WAV to enable TestWhisperTranscribeIntegration
with an installed model and a speech WAV. WHISPER_TEST_BACKEND selects a forced
backend; WHISPER_EXPECT_BACKEND optionally checks which backend actually ran.
