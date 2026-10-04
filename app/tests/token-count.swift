import Foundation
@main struct Tests {
    static func main() {
        for (text,expected) in [("8192",8192),("32K",32000),("128k",128000),(" 1M ",1000000),("1.5m",1500000),("0.001K",1),("4294967296",4294967296),("9007199254740991",TokenCount.maximum)] {
            assert(TokenCount.parse(text)==expected,"Failed: "+text)
        }
        for text in ["","0","-1","1.5","0.0001K","1e6","1G","NaN","1KK","9007199254740992","99999999999999999999999999999999999999999999999999999999"] {
            assert(TokenCount.parse(text)==nil,"Accepted: "+text)
        }
        print("Token counts: decimal K/M, fractions, large values and invalid/overflow inputs passed")
    }
}
