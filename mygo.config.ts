// The portable preview uses a separate app name and ID, with no release feed
// or URL scheme. MyGo keys its user data directory by the app name.
export default {
  name: "MyGo Music zh-TW Preview",
  identifier: "local.mygo.music.zhtwpreview",
  slug: "mygo-music-zh-tw-preview",
  version: "0.1.22",
  out: "build/preview",
  macos: {
    icon: "icon/AppIcon.icon",
    minimumSystemVersion: "13.0",
    signingIdentity: "Developer ID Application: xinyu zeng (GJE9R5VE87)",
    notarize: { keychainProfile: "notary" },
  },
  linux: {
    comment: "A music player for your files and more",
    categories: ["AudioVideo", "Audio", "Player"],
    webview: false,
  },
};
