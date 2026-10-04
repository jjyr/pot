import Foundation

struct UnreadTracker {
    private(set) var seen: Set<String> = []
    private(set) var initialized = false
    mutating func update(completed: Set<String>, saved: [String]?, readingLatest: Bool) -> Int {
        if !initialized {
            seen = Set(saved ?? Array(completed)); initialized = true
        }
        if readingLatest {
            seen.formUnion(completed)
        }
        return completed.subtracting(seen).count
    }
}
