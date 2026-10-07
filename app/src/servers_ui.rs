use crate::{
    Shell, client,
    connections::{self, LOCAL},
    gpui, navigation, ui,
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    Disableable, Sizable,
    button::{Button, ButtonVariants},
    input::{Input, InputState},
    menu::{DropdownMenu, PopupMenuItem},
};
use serde_json::json;
use std::{
    collections::{HashMap, HashSet},
    sync::{Arc, Mutex},
};

pub struct Session {
    client: client::Client,
    history: navigation::History<navigation::State>,
    settings: ui::Settings,
    schedules: crate::schedules_ui::SchedulesUI,
    usage: crate::usage_ui::UsageUI,
    scroll: ScrollHandle,
    tasks_scroll: ScrollHandle,
    search: Entity<InputState>,
    show_tasks: bool,
    schedule_tab: bool,
    expanded: HashSet<String>,
    page: u64,
}
pub struct Servers {
    pub sessions: HashMap<String, Session>,
    pub pending_switch: Option<String>,
    fields: Vec<Entity<InputState>>,
    open: bool,
    editing: Option<String>,
    pairing: bool,
    notice: String,
    pending: bool,
    result: Arc<Mutex<Option<Result<String, String>>>>,
    remove: Option<String>,
}
impl Servers {
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        Self {
            sessions: HashMap::new(),
            pending_switch: None,
            fields: (0..4)
                .map(|_| cx.new(|cx| InputState::new(window, cx)))
                .collect(),
            open: false,
            editing: None,
            pairing: true,
            notice: String::new(),
            pending: false,
            result: Default::default(),
            remove: None,
        }
    }
    pub fn poll(&mut self, active: &str) -> bool {
        let result = self.result.lock().unwrap().take();
        if let Some(result) = result {
            self.pending = false;
            match result {
                Ok(id) => {
                    self.open = false;
                    self.notice = crate::i18n::text("servers.saved").into();
                    // Refresh transport after editing/re-pairing; keep conversation and navigation state.
                    if id == active {
                        self.pending_switch = Some(id);
                    }
                }
                Err(e) => self.notice = e,
            }
            return true;
        }
        false
    }
}
impl Shell {
    pub fn switch_server(&mut self, id: &str, window: &mut Window, cx: &mut Context<Self>) {
        let target = match connections::target(id) {
            Ok(t) => t,
            Err(e) => {
                self.servers.notice = e;
                return;
            }
        };
        let current = self.client.target.id().to_owned();
        if id == current {
            // A changed endpoint or a fresh key gets a new transport, while retaining its View.
            self.client.retarget(target);
            self.client.refresh();
            return;
        }
        self.observe_navigation();
        let mut session = self.servers.sessions.remove(id).unwrap_or_else(|| Session {
            client: client::Client::start(target.clone()),
            history: Default::default(),
            settings: ui::Settings::new(window, cx),
            schedules: crate::schedules_ui::SchedulesUI::new(window, cx),
            usage: crate::usage_ui::UsageUI::new(self.catalog(), window, cx),
            scroll: ScrollHandle::new(),
            tasks_scroll: ScrollHandle::new(),
            search: cx.new(|cx| InputState::new(window, cx)),
            show_tasks: false,
            schedule_tab: false,
            expanded: HashSet::new(),
            page: 0,
        });
        session.client.retarget(target);
        let page = session.page;
        let saved = Session {
            client: std::mem::replace(&mut self.client, session.client),
            history: std::mem::replace(&mut self.history, session.history),
            settings: std::mem::replace(&mut self.settings, session.settings),
            schedules: std::mem::replace(&mut self.schedules_ui, session.schedules),
            usage: std::mem::replace(&mut self.usage_ui, session.usage),
            scroll: std::mem::replace(&mut self.scroll, session.scroll),
            tasks_scroll: std::mem::replace(&mut self.tasks_scroll, session.tasks_scroll),
            search: std::mem::replace(&mut self.task_search, session.search),
            show_tasks: std::mem::replace(&mut self.show_tasks, session.show_tasks),
            schedule_tab: std::mem::replace(&mut self.schedule_tab, session.schedule_tab),
            expanded: std::mem::replace(&mut self.expanded, session.expanded),
            page: self.native["page"].as_u64().unwrap_or(0),
        };
        saved.client.set_visible(false);
        self.client.set_visible(true);
        self.servers.sessions.insert(current, saved);
        self.pending_offsets = None;
        self.pending_task_anchor = None;
        self.pending_history = None;
        self.last_view = None;
        self.last_content.clear();
        self.summary_count = 0;
        self.summary_removal = None;
        self.first = false;
        self.jumping_to_latest = false;
        ui::command(json!({"action":"server-activate","id":id,"page":page}));
        let mut buffer = vec![0i8; 524288];
        if unsafe { crate::pot_bridge_ui(buffer.as_mut_ptr(), buffer.len() as i32) } > 0 {
            self.native = serde_json::from_slice(
                unsafe { std::ffi::CStr::from_ptr(buffer.as_ptr()) }.to_bytes(),
            )
            .unwrap_or_default();
        }
        self.client.refresh();
        cx.notify();
    }
    pub fn server_picker(&self, _cx: &mut Context<Self>) -> impl IntoElement {
        let selected = self.client.target.id().to_owned();
        let list = connections::list();
        let name = if selected == LOCAL {
            crate::i18n::text("servers.local").to_owned()
        } else {
            list.iter()
                .find(|c| c.id == selected)
                .map(|c| c.name.clone())
                .unwrap_or_default()
        };
        Button::new("server-picker")
            .ghost()
            .small()
            .label(format!(
                "{name}{} ▾",
                if self.client.view.lock().unwrap().error.is_empty() {
                    String::new()
                } else {
                    format!(" · {}", crate::i18n::text("servers.offline"))
                }
            ))
            .accessibility_label(crate::i18n::text("servers.switch"))
            .dropdown_menu(move |menu, _, _| {
                let menu = menu.item(
                    PopupMenuItem::new(crate::i18n::text("servers.local"))
                        .checked(selected == LOCAL)
                        .on_click(|_, _, _| {
                            ui::command(json!({"action":"server-select","id":LOCAL}))
                        }),
                );
                list.iter().fold(menu, |menu, c| {
                    let id = c.id.clone();
                    menu.item(
                        PopupMenuItem::new(c.name.clone())
                            .checked(selected == c.id)
                            .on_click(move |_, _, _| {
                                ui::command(json!({"action":"server-select","id":id}))
                            }),
                    )
                })
            })
            .into_any_element()
    }
    fn server_form(
        &mut self,
        id: Option<String>,
        pairing: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let connection = id
            .as_ref()
            .and_then(|id| connections::list().into_iter().find(|c| &c.id == id));
        let values = [
            connection
                .as_ref()
                .map(|c| c.address.clone())
                .unwrap_or_default(),
            String::new(),
            connection
                .as_ref()
                .map(|c| c.name.clone())
                .unwrap_or_default(),
            "Pot Mac".into(),
        ];
        for (field, value) in self.servers.fields.iter().zip(values) {
            field.update(cx, |f, cx| f.set_value(value, window, cx));
        }
        self.servers.editing = id;
        self.servers.pairing = pairing;
        self.servers.open = true;
        self.servers.notice.clear();
        cx.notify();
    }
    fn save_server(&mut self, cx: &mut Context<Self>) {
        if self.servers.pending {
            return;
        }
        let fields: Vec<String> = self
            .servers
            .fields
            .iter()
            .map(|f| f.read(cx).value().to_string())
            .collect();
        let editing = self.servers.editing.clone();
        let pairing = self.servers.pairing;
        let result = self.servers.result.clone();
        self.servers.pending = true;
        self.servers.notice.clear();
        std::thread::spawn(move || {
            let reply = if pairing {
                connections::pair(
                    &fields[0],
                    &fields[1],
                    &fields[3],
                    &fields[2],
                    editing.as_deref(),
                )
            } else {
                connections::edit(editing.as_deref().unwrap_or(""), &fields[2], &fields[0])
                    .map(|_| editing.unwrap_or_default())
            };
            *result.lock().unwrap() = Some(reply);
            crate::wake_ui();
        });
        cx.notify();
    }
    pub fn servers_page(&self, cx: &mut Context<Self>) -> Div {
        let mut list = div()
            .flex()
            .flex_col()
            .gap_3()
            .child(ui::card(cx).child(crate::i18n::text("servers.local")))
            .child(ui::hint(crate::i18n::text("servers.local-hint"), cx));
        for connection in connections::list() {
            let id = connection.id.clone();
            let edit_id = id.clone();
            let repair_id = id.clone();
            let remove_id = id.clone();
            let view = if self.client.target.id() == id {
                Some(self.client.view.clone())
            } else {
                self.servers
                    .sessions
                    .get(&id)
                    .map(|s| s.client.view.clone())
            };
            let status = view
                .as_ref()
                .map(|v| {
                    if v.lock().unwrap().error.is_empty() {
                        "servers.connected"
                    } else {
                        "servers.offline"
                    }
                })
                .unwrap_or("servers.not-connected");
            list = list.child(
                ui::card(cx).child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .flex()
                                .flex_col()
                                .child(div().truncate().child(connection.name.clone()))
                                .child(ui::hint(connection.address.clone(), cx).truncate()),
                        )
                        .child(ui::hint(crate::i18n::text(status), cx))
                        .child(
                            Button::new(SharedString::from(format!("server-use-{}", id)))
                                .ghost()
                                .small()
                                .label(crate::i18n::text("servers.switch"))
                                .on_click(move |_, _, _| {
                                    ui::command(json!({"action":"server-select","id":id}))
                                }),
                        )
                        .child(
                            Button::new(SharedString::from(format!("server-edit-{}", edit_id)))
                                .ghost()
                                .small()
                                .label(crate::i18n::text("servers.edit"))
                                .disabled(self.servers.pending)
                                .on_click(cx.listener(move |this, _, window, cx| {
                                    this.server_form(Some(edit_id.clone()), false, window, cx)
                                })),
                        )
                        .child(
                            Button::new(SharedString::from(format!("server-repair-{}", repair_id)))
                                .ghost()
                                .small()
                                .label(crate::i18n::text("servers.repair"))
                                .disabled(self.servers.pending)
                                .on_click(cx.listener(move |this, _, window, cx| {
                                    this.server_form(Some(repair_id.clone()), true, window, cx)
                                })),
                        )
                        .child(
                            Button::new(SharedString::from(format!("server-remove-{}", remove_id)))
                                .ghost()
                                .small()
                                .label(crate::i18n::text("servers.remove"))
                                .disabled(self.servers.pending)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.servers.remove = Some(remove_id.clone());
                                    cx.notify();
                                })),
                        ),
                ),
            );
        }
        if let Some(id) = &self.servers.remove {
            let id = id.clone();
            list = list.child(
                ui::card(cx)
                    .child(ui::hint(crate::i18n::text("servers.remove-hint"), cx))
                    .child(
                        Button::new("confirm-remove")
                            .primary()
                            .small()
                            .label(crate::i18n::text("servers.remove"))
                            .on_click(cx.listener(move |this, _, window, cx| {
                                if let Err(e) = connections::remove(&id) {
                                    this.servers.notice = e;
                                } else {
                                    if this.client.target.id() == id {
                                        this.switch_server(LOCAL, window, cx);
                                    }
                                    this.servers.sessions.remove(&id);
                                    this.servers.remove = None;
                                }
                                cx.notify();
                            })),
                    )
                    .child(
                        Button::new("cancel-remove")
                            .ghost()
                            .small()
                            .label(crate::i18n::text("schedules-ui.cancel"))
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.servers.remove = None;
                                cx.notify();
                            })),
                    ),
            );
        }
        if self.servers.open {
            let mut form = ui::form(cx)
                .child(
                    ui::field(crate::i18n::text("servers.address")).child(
                        Input::new(&self.servers.fields[0])
                            .aria_label(crate::i18n::text("servers.address")),
                    ),
                )
                .child(
                    ui::field(crate::i18n::text("servers.name")).child(
                        Input::new(&self.servers.fields[2])
                            .aria_label(crate::i18n::text("servers.name")),
                    ),
                );
            if self.servers.pairing {
                form = form
                    .child(
                        ui::field(crate::i18n::text("servers.code")).child(
                            Input::new(&self.servers.fields[1])
                                .aria_label(crate::i18n::text("servers.code")),
                        ),
                    )
                    .child(
                        ui::field(crate::i18n::text("servers.device")).child(
                            Input::new(&self.servers.fields[3])
                                .aria_label(crate::i18n::text("servers.device")),
                        ),
                    )
                    .child(
                        ui::field(crate::i18n::text("servers.pair-hint"))
                            .child(ui::hint("pot access pair", cx)),
                    );
            }
            list = list.child(
                ui::card(cx).child(form).child(
                    div()
                        .flex()
                        .gap_2()
                        .justify_end()
                        .child(
                            Button::new("server-cancel")
                                .ghost()
                                .small()
                                .label(crate::i18n::text("schedules-ui.cancel"))
                                .disabled(self.servers.pending)
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.servers.open = false;
                                    cx.notify();
                                })),
                        )
                        .child(
                            Button::new("server-save")
                                .primary()
                                .small()
                                .label(crate::i18n::text(if self.servers.pending {
                                    "servers.connecting"
                                } else if self.servers.pairing {
                                    "servers.connect"
                                } else {
                                    "client.saved"
                                }))
                                .disabled(self.servers.pending)
                                .on_click(cx.listener(|this, _, _, cx| this.save_server(cx))),
                        ),
                ),
            );
        } else {
            list = list.child(
                Button::new("server-add")
                    .primary()
                    .small()
                    .label(crate::i18n::text("servers.add"))
                    .on_click(
                        cx.listener(|this, _, window, cx| this.server_form(None, true, window, cx)),
                    ),
            );
        }
        list.when(!self.servers.notice.is_empty(), |list| {
            list.child(ui::notice(self.servers.notice.clone(), false, cx))
        })
    }
}
