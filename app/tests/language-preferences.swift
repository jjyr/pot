import Foundation

@main struct Tests {
    static func main() {
        let previous = UserDefaults.standard.string(forKey: "language")
        defer {
            if let previous {
                UserDefaults.standard.set(previous, forKey: "language")
            } else {
                UserDefaults.standard.removeObject(forKey: "language")
            }
        }
        for (languages, expected) in [
            (["zh-Hans-CN", "en-US"], "zh-CN"),
            (["zh-TW"], "zh-CN"),
            (["ZH_hant_HK"], "zh-CN"),
            (["en-GB", "zh-CN"], "en"),
            (["fr-FR", "zh-CN"], "zh-CN"),
            (["fr-FR", "de-DE"], "en"),
            ([String](), "en"),
            (["zhuang", "english"], "en"),
        ] {
            assert(L.resolve(languages) == expected)
        }
        assert(L.resolve(saved: "en", languages: ["zh-CN"]) == "en")
        assert(L.resolve(saved: "zh-CN", languages: ["en-US"]) == "zh-CN")
        assert(L.resolve(saved: "invalid", languages: ["zh-CN"]) == "zh-CN")
        L.select("en")
        assert(!L.select("unsupported"))
        assert(L.locale == "en")
        assert(L.text("settings.language") == "Language")
        var notice = LocalizedMessage(wrappedValue: L.format("settingspanel.command-not-saved-0", ["HTTP 401: {1}"]))
        assert(notice.wrappedValue == "Command not saved: HTTP 401: {1}")
        let secret = "user content {0} 简体中文"
        assert(L.format("authorization.enter-code-0-on-the-official-page", [secret]).contains(secret))
        L.select("zh-CN")
        assert(UserDefaults.standard.string(forKey: "language") == "zh-CN")
        assert(L.text("settings.language") == "语言")
        assert(notice.wrappedValue == "命令未保存：HTTP 401: {1}")
        notice.wrappedValue = "user content must stay intact"
        L.select("en")
        assert(notice.wrappedValue == "user content must stay intact")
        assert(L.message("请提供 API key") == "Provide an API key")
        assert(L.message("opaque provider error") == "opaque provider error")
        assert(L.text("unknown.key") == "unknown.key")
        print("Localization: system matching, fallback, saved preference, live notices and literal parameters passed")
    }
}
