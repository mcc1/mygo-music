# YouTube signature solver

These files are the unmodified EJS 0.8.0 release assets from
[yt-dlp/ejs](https://github.com/yt-dlp/ejs/releases/tag/0.8.0). The
asset headers contain the licenses for the solver and bundled dependencies.
MyGo tests their SHA3-512 digests against the hashes published by
[yt-dlp](https://github.com/yt-dlp/yt-dlp/blob/master/yt_dlp/extractor/youtube/jsc/_builtin/vendor/_info.py).

MyGo runs the embedded solver in a local Deno process with network, file,
environment, subprocess and FFI permissions denied. Its inputs are the public
YouTube player script and the signature and throttling challenges. MyGo
constructs the final media URL outside Deno.
