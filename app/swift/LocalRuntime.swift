import Foundation
import AppKit
import WebKit
import Darwin

// App-owned user process; bundle-relative executable/resources, private Unix IPC.
@MainActor final class LocalRuntime {
    let process = Process()
    let root: URL
    weak var web: WKWebView?
    let label = NSTextField(wrappingLabelWithString: "Local runtime: starting helper")
    var timer: Timer?
    var polling = false
    var probing = false
    var loaded = ""
    var onState: (([String:Any])->Void)?
    init(view: NSView, web: WKWebView) {
        self.web = web
        root = URL(fileURLWithPath: ProcessInfo.processInfo.environment["POT_LOCAL_ROOT"] ?? NSHomeDirectory()+"/Library/Application Support/pot")
        do {
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            label.frame = NSRect(x:16,y:120,width:300,height:110);label.textColor = .white;// Native chat presents status
            let start = NSButton(title:"Start local desktop",target:self,action:#selector(startVM));start.bezelStyle = .rounded;start.appearance=NSAppearance(named:.darkAqua);start.frame=NSRect(x:16,y:70,width:160,height:32);// Start is automatic
            let stop = NSButton(title:"Stop",target:self,action:#selector(stopVM));stop.bezelStyle = .rounded;stop.appearance=NSAppearance(named:.darkAqua);stop.frame=NSRect(x:185,y:70,width:100,height:32);// Stop via Quit
            process.executableURL=Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/PotHelper")
            process.arguments=[root.path,Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/runtime").path]
            let log=root.appendingPathComponent("helper.log");FileManager.default.createFile(atPath:log.path,contents:nil)
            let handle=try FileHandle(forWritingTo:log);process.standardOutput=handle;process.standardError=handle
            try process.run()
            DispatchQueue.main.asyncAfter(deadline:.now()+1) {self.send("start")}
            timer=Timer.scheduledTimer(withTimeInterval:1,repeats:true) { [weak self] _ in MainActor.assumeIsolated {self?.poll()} }
        } catch {label.stringValue="Helper failed: \(error)"}
    }
    nonisolated static func request(path:String, method:String) -> [String:Any] {
        let fd=socket(AF_UNIX,SOCK_STREAM,0);defer{close(fd)}
        var timeout=timeval(tv_sec:15,tv_usec:0)
        setsockopt(fd,SOL_SOCKET,SO_RCVTIMEO,&timeout,socklen_t(MemoryLayout<timeval>.size))
        var addr=sockaddr_un();addr.sun_family=sa_family_t(AF_UNIX)
        guard path.utf8.count<104 else{return ["error":"Socket path too long"]}
        withUnsafeMutableBytes(of:&addr.sun_path){dest in path.utf8CString.withUnsafeBytes{dest.copyBytes(from:$0)}}
        let result=withUnsafePointer(to:&addr){$0.withMemoryRebound(to:sockaddr.self,capacity:1){connect(fd,$0,socklen_t(MemoryLayout<sockaddr_un>.size))}}
        guard result==0 else{return ["error":"Helper IPC unavailable"]}
        var data=try! JSONSerialization.data(withJSONObject:["id":UUID().uuidString,"method":method]);data.append(10)
        data.withUnsafeBytes{_ = Darwin.write(fd,$0.baseAddress,data.count)}
        var response=Data();var byte:UInt8=0
        while response.count<65536 && read(fd,&byte,1)==1 {if byte==10{break};response.append(byte)}
        return (try? JSONSerialization.jsonObject(with:response)) as? [String:Any] ?? ["error":"Invalid helper response"]
    }
    func send(_ method:String, completion: (([String:Any])->Void)? = nil) {
        let path=root.appendingPathComponent("helper.sock").path
        DispatchQueue.global().async {let response=Self.request(path:path,method:method);DispatchQueue.main.async{self.render(response);completion?(response)}}
    }
    func poll() {
        guard process.isRunning else {label.stringValue="Helper exited (\(process.terminationStatus)); reopen App to recover";timer?.invalidate();return}
        if polling{return};polling=true
        let path=root.appendingPathComponent("helper.sock").path
        DispatchQueue.global().async {let response=Self.request(path:path,method:"status");DispatchQueue.main.async{self.polling=false;self.render(response)}}
    }
    func render(_ response:[String:Any]) {
        guard let state=response["result"] as? [String:Any] else {label.stringValue=String(describing:response["error"] ?? "IPC error");return}
        let phase=state["phase"] as? String ?? "unknown"
        onState?(state)
        label.stringValue="Local runtime: \(phase)\n\(state["error"] ?? "")"
        if phase=="running", let address=state["viewer"] as? String, !address.isEmpty, address != loaded, !probing, let url=URL(string:address) {
            // Guest package installation is asynchronous: probe HTTP before loading.
            probing=true
            URLSession.shared.dataTask(with:URLRequest(url:url,cachePolicy:.reloadIgnoringLocalCacheData,timeoutInterval:3)) { [weak self] _,response,_ in
                DispatchQueue.main.async {self?.probing=false}
                guard (response as? HTTPURLResponse)?.statusCode==200 else{return}
                DispatchQueue.main.async {guard let self, self.loaded != address else{return};self.loaded=address;var components=URLComponents(url:url,resolvingAgainstBaseURL:false)!;components.queryItems=[URLQueryItem(name:"native",value:"1")];self.web?.load(URLRequest(url:components.url!,cachePolicy:.reloadIgnoringLocalCacheData));self.label.stringValue="Local desktop ready"}
            }.resume()
        }
        if phase=="stopped" || phase=="failed" || phase=="stopping" {loaded="";onState?(state)}
    }
    @objc func startVM(){send("start")}
    @objc func stopVM(){send("stop")}
    func cleanup(){timer?.invalidate();timer=nil;if process.isRunning {process.terminate()}}
}
