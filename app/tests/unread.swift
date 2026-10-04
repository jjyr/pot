import Foundation

@main struct Tests {
    static func main(){
        var tracker=UnreadTracker()
        assert(tracker.update(completed:["old"],saved:nil,readingLatest:false)==0)
        assert(tracker.update(completed:["old","new"],saved:nil,readingLatest:false)==1)
        assert(tracker.update(completed:["old","new"],saved:nil,readingLatest:false)==1)
        // Opening the window on Sandbox or reading old history must not clear it.
        assert(tracker.update(completed:["old","new"],saved:nil,readingLatest:false)==1)
        assert(tracker.update(completed:["old","new"],saved:nil,readingLatest:true)==0)
        var reopened=UnreadTracker()
        assert(reopened.update(completed:["old","new","later"],saved:Array(tracker.seen),readingLatest:false)==1)
        var fresh=UnreadTracker()
        assert(fresh.update(completed:[],saved:[],readingLatest:false)==0)
        assert(fresh.update(completed:["first"],saved:[],readingLatest:false)==1)
        print("Unread state: baseline, duplicates, hidden/history, read, reopen passed")
    }
}
