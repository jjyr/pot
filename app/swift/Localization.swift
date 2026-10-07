import Foundation

/// One app-wide language. A missing preference is resolved once from macOS languages.
enum L {
    private static let preferenceKey = "language"
    static var lastTranslation: LocalizedValue?
    static var locale: String = initialLocale()

    static func phase(_ phase: String) -> String {
        if phase == "unavailable" {
            return text("web.unavailable")
        }
        let key = ["starting", "running", "stopping", "stopped", "failed"].contains(phase) ? phase : "unknown"
        return text("sandbox.phase." + key)
    }

    static func resolve(_ languages: [String]) -> String {
        for language in languages {
            let normalized = language.replacingOccurrences(of: "_", with: "-").lowercased()
            let available = TranslationResources.values.keys.sorted()
            if let exact = available.first(where: { $0.lowercased() == normalized }) {
                return exact
            }
            if let compatible = available
                .first(where: { $0.lowercased().split(separator: "-").first == normalized.split(separator: "-").first
                })
            {
                return compatible
            }
        }
        return "en"
    }

    static func resolve(saved: String?, languages: [String]) -> String {
        if let saved, TranslationResources.values[saved] != nil {
            return saved
        }
        return resolve(languages)
    }

    private static func initialLocale() -> String {
        let defaults = UserDefaults.standard
        let selected = resolve(saved: defaults.string(forKey: preferenceKey), languages: Locale.preferredLanguages)
        defaults.set(selected, forKey: preferenceKey)
        return selected
    }

    @discardableResult static func select(_ language: String) -> Bool {
        guard TranslationResources.values[language] != nil, language != locale else { return false }
        locale = language
        UserDefaults.standard.set(language, forKey: preferenceKey)
        return true
    }

    static func raw(_ key: String) -> String {
        TranslationResources.values[locale]?[key] ?? TranslationResources.values["en"]?[key] ?? key
    }

    static func text(_ key: String) -> String {
        let value = LocalizedValue(key: key, arguments: [])
        let rendered = value.render()
        lastTranslation = value
        return rendered
    }

    static func message(_ text: String) -> String {
        for values in TranslationResources.values.values {
            if let key = values.first(where: { $0.value == text })?.key {
                return self.text(key)
            }
        }
        return text
    }

    static func format(_ key: String, _ arguments: [String]) -> String {
        // Replace in one pass: user-provided braces must never become placeholders.
        let value = LocalizedValue(key: key, arguments: arguments)
        let rendered = value.render()
        lastTranslation = value
        return rendered
    }

    static func interpolate(_ key: String, _ arguments: [String]) -> String {
        let template = raw(key)
        guard let regex = try? NSRegularExpression(pattern: "\\{([0-9]+)\\}") else { return template }
        let source = template as NSString
        var result = template
        for match in regex.matches(in: template, range: NSRange(location: 0, length: source.length)).reversed() {
            guard let index = Int(source.substring(with: match.range(at: 1))), index < arguments.count,
                  let range = Range(match.range, in: result) else { continue }
            result.replaceSubrange(range, with: arguments[index])
        }
        return result
    }
}

struct LocalizedValue {
    let key: String
    let arguments: [String]
    func render() -> String {
        L.interpolate(key, arguments)
    }
}

/// Captures translation identity at assignment so saved notices follow language changes.
@propertyWrapper struct LocalizedMessage {
    private var source = ""
    private var translation: LocalizedValue?
    init(wrappedValue: String) {
        self.wrappedValue = wrappedValue
    }

    var wrappedValue: String {
        get { translation?.render() ?? L.message(source) }
        set {
            source = newValue
            translation = L.lastTranslation.flatMap { $0.render() == newValue ? $0 : nil }
        }
    }
}
