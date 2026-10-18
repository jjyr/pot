import {node as n,field,action} from './primitives.mjs';

export function notificationSetting({notifications,locale,desktop}) {
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const root=n('section','notification-setting');
  const toggle=field(tr('System notifications','系统通知'),true,{kind:'checkbox'});
  toggle.row.classList.add('preference-setting');
  const status=n('p','hint');status.setAttribute('role','status');
  const request=action(tr('Allow notifications','授权通知'),async()=>{
    request.disabled=true;
    try{await notifications.authorize();}catch(reason){status.textContent=reason.message;}finally{request.disabled=false;}
  });
  root.append(toggle.row,status,request,n('p','hint',tr('Saved on this device for this client. Sound, banners and Do Not Disturb follow your browser and system settings.','仅保存在此设备的当前客户端。声音、横幅和勿扰由浏览器及系统设置控制。')));
  function update(){
    const {enabled,permission,error}=notifications.state;toggle.input.checked=enabled;
    const messages={loading:tr('Checking notification permission…','正在检查通知权限…'),
      granted:tr('Permission granted. Delivery follows system notification settings.','已获授权，实际展示遵循系统通知设置。'),
      default:tr('Authorization required. Use Allow notifications to request permission.','需要授权。点击“授权通知”请求权限。'),
      denied:desktop?tr('Permission denied. Open macOS System Settings → Notifications → Pot to allow notifications.','通知权限被拒绝。请前往 macOS 系统设置 → 通知 → Pot 开启。'):tr('Permission denied. Open this site’s settings in Chrome → Notifications → Allow, then return here.','通知权限被拒绝。请在 Chrome 此网站的设置 → 通知中选择允许，然后返回此页。'),
      unsupported:tr('This browser does not support system notifications. Use a supported browser such as Chrome.','此浏览器不支持系统通知，请使用 Chrome 等支持的浏览器。'),
      insecure:tr('Notifications require HTTPS or localhost.','通知需要 HTTPS 或 localhost 安全上下文。'),
      unavailable:tr('System notifications are unavailable.','系统通知暂不可用。')};
    status.textContent=[!enabled?tr('Off on this client.','此客户端已关闭通知。'):messages[permission],error].filter(Boolean).join(' ');
    request.hidden=!enabled||permission!=='default';
  }
  toggle.input.onchange=async()=>{toggle.input.disabled=true;try{await notifications.setEnabled(toggle.input.checked);}catch(reason){status.textContent=reason.message;toggle.input.checked=notifications.state.enabled;}finally{toggle.input.disabled=false;}};
  const dispose=notifications.subscribe(update);update();
  return {element:root,dispose};
}
