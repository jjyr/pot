import Foundation

// Decimal SI suffixes, matching model token-count notation (1K = 1000).
enum TokenCount {
    static let maximum = 9_007_199_254_740_991 // Exact JSON integer in Pi/JavaScript.
    static func parse(_ input:String)->Int? {
        let text=input.trimmingCharacters(in:.whitespacesAndNewlines).uppercased()
        guard text.range(of:"^[0-9]+(?:\\.[0-9]+)?[KM]?$",options:.regularExpression) != nil else{return nil}
        let suffix=text.last
        let multiplier = suffix == "K" ? 1000 : suffix == "M" ? 1000000 : 1
        let number=multiplier == 1 ? text:String(text.dropLast())
        guard let value=Decimal(string:number,locale:Locale(identifier:"en_US_POSIX")) else{return nil}
        var count=value*Decimal(multiplier)
        var integer=Decimal()
        NSDecimalRound(&integer,&count,0,.plain)
        guard count==integer,count>=1,count<=Decimal(maximum) else{return nil}
        return NSDecimalNumber(decimal:count).intValue
    }
}
