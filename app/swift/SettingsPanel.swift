import AppKit
import Foundation

// Configuration service only. GPUI Kit owns the settings interface.
@MainActor final class SettingsPanel: NSObject {
    weak var bridge: PotBridge?
    var environment:[String:Any]=[:]
    var section=0, revision=0
    var setup=false, hasConfiguration=false
    var config:[String:Any]=[:]
    var awaitingBoot:String?
    var cpu=4, memory=8, disk=32
    var key="", limit="10000", tokens="8192", perTurn="100", parallel="5"
    var cpuField="4", memoryField="8", diskField="32", result=""
    var root:URL {bridge?.localRuntime?.root ?? URL(fileURLWithPath:NSHomeDirectory()+"/Library/Application Support/pot")}
    override init(){super.init();changeAppearance(UserDefaults.standard.integer(forKey:"appearance"))}
    func showAgent(setup:Bool=false){self.setup=setup;section=1}
    func changeAppearance(_ value:Int){
        guard (0...2).contains(value) else{return}
        UserDefaults.standard.set(value,forKey:"appearance")
        NSApp.appearance=value == 0 ? nil:NSAppearance(named:value == 1 ? .aqua:.darkAqua)
    }
    func refreshStatus(){
        guard let bridge else{return}
        if let old=awaitingBoot,let boot=bridge.snapshot["boot_id"] as? String,boot != old,bridge.snapshot["connected"] as? Bool == true,bridge.localPhase == "running" {result="配置已应用，环境已就绪。";awaitingBoot=nil}
    }
    var publicState:[String:Any] {
        ["revision":revision,"section":section,"setup":setup,"has_key":hasConfiguration,
         "limit":limit,"tokens":tokens,"per_turn":perTurn,"parallel":parallel,"cpus":cpuField,"memory":memoryField,"disk":diskField,
         "appearance":UserDefaults.standard.integer(forKey:"appearance"),"result":result]
    }
    func load(){
        config=((try? Data(contentsOf:root.appendingPathComponent("workspace/product/pi.json"))).flatMap{try? JSONSerialization.jsonObject(with:$0)} as? [String:Any]) ?? [:]
        hasConfiguration = !(config["api_key"] as? String ?? "").isEmpty
        key="";
        limit=String(config["request_limit"] as? Int ?? 10000)
        tokens=String(config["max_tokens"] as? Int ?? 8192)
        perTurn=String(config["max_requests_per_turn"] as? Int ?? 100)
        parallel=String(config["max_parallel_tasks"] as? Int ?? 5)
        if let data=try? Data(contentsOf:root.appendingPathComponent("sandbox.json")),let value=(try? JSONSerialization.jsonObject(with:data)) as? [String:Int] {cpu=value["cpus"] ?? 4;memory=value["memory_gib"] ?? 8;disk=value["disk_gib"] ?? 32}
        cpuField=String(cpu);memoryField=String(memory);diskField=String(disk)
        revision += 1
    }
    func parsed(_ field:String,_ range:ClosedRange<Int>)->Int?{guard let value=Int(field),range.contains(value) else{return nil};return value}
    @objc func saveAgent(){
        guard let l=parsed(limit,0...1000000),let t=TokenCount.parse(tokens),let p=parsed(perTurn,1...1000),let n=parsed(parallel,1...1000) else{result="请检查请求上限；输出 token 可填正整数或 K/M，例如 128K、1M、1.5M。";return}
        let credential=key.isEmpty ? config["api_key"] as? String ?? "":key.trimmingCharacters(in:.whitespacesAndNewlines)
        guard !credential.isEmpty,!credential.contains("\n") else{result="需要有效的 DeepSeek API key；密钥不会显示或写入日志。";return}
        let value:[String:Any]=["api_key":credential,"model":"deepseek-v4-flash","request_limit":l,"max_tokens":t,"max_requests_per_turn":p,"max_parallel_tasks":n]
        apply(value,to:root.appendingPathComponent("workspace/product/pi.json"))
    }
    @objc func saveSandbox(){
        guard let c=parsed(cpuField,1...32),let m=parsed(memoryField,1...128),let d=parsed(diskField,4...512) else{result="请输入范围内的 CPU、内存与系统盘容量。";return}
        apply(["cpus":c,"memory_gib":m,"disk_gib":d],to:root.appendingPathComponent("sandbox.json"))
    }
    func apply(_ value:[String:Any],to path:URL){
        guard let bridge,!bridge.running,!bridge.isApplying else{result="任务仍在运行或环境正在更新，请结束后再应用。";return}
        guard ["running","stopped","failed"].contains(bridge.localPhase) else{result="环境正在切换状态，请稍后再试。";return}
        // A disconnected Runtime cannot prove that there is no active task.
        guard bridge.localPhase != "running" || bridge.snapshot["connected"] as? Bool == true else{result="Runtime 尚未连接，无法确认任务状态，请稍后重试。";return}
        let alert=NSAlert();alert.messageText=setup ? "保存配置并启动 Pot？":"应用配置并重启 Sandbox？"
        alert.informativeText="当前没有运行中的任务。聊天、工作文件和用量记录会保留；浏览器将正常关闭。此操作不调用模型。"
        alert.addButton(withTitle:"应用");alert.addButton(withTitle:"取消")
        alert.beginSheetModal(for:bridge.host){ [weak self] response in
            MainActor.assumeIsolated {
                guard response == .alertFirstButtonReturn,let self,let bridge=self.bridge else{return}
                guard !bridge.running else{self.result="刚有任务开始，请完成后再应用。";return}
                bridge.isApplying=true;bridge.ready=false;bridge.sendButton.isEnabled=false
                self.result="正在正常停止环境并保存配置…"
                guard let runtime=bridge.localRuntime else{return}
                runtime.send("stop") { [weak self] response in
                    guard let self,let bridge=self.bridge else{return}
                    guard let state=response["result"] as? [String:Any],state["phase"] as? String == "stopped" else{bridge.isApplying=false;self.result="停止失败，配置未修改。";return}
                    do {
                        try FileManager.default.createDirectory(at:path.deletingLastPathComponent(),withIntermediateDirectories:true)
                        let data=try JSONSerialization.data(withJSONObject:value,options:.sortedKeys)
                        // Atomic replacement and mode 0600; never include credentials in UI errors.
                        let temp=path.deletingLastPathComponent().appendingPathComponent(".config-"+UUID().uuidString)
                        guard FileManager.default.createFile(atPath:temp.path,contents:nil,attributes:[.posixPermissions:0o600]) else{throw NSError(domain:"Pot",code:1)}
                        defer {try? FileManager.default.removeItem(at:temp)}
                        let handle=try FileHandle(forWritingTo:temp);try handle.write(contentsOf:data);try handle.synchronize();try handle.close()
                        guard rename(temp.path,path.path)==0 else{throw NSError(domain:"Pot",code:2)}
                        self.awaitingBoot=bridge.snapshot["boot_id"] as? String ?? ""
                        self.load();self.result="配置已保存，正在启动环境…";self.setup=false
                        runtime.send("start"){ _ in bridge.isApplying=false }
                    } catch {bridge.isApplying=false;self.result="保存失败，原配置保留。可从 Sandbox 重新启动环境。"}
                }
            }
        }
    }
    @objc func openWorkspace(){bridge?.openWorkspace()}
}
