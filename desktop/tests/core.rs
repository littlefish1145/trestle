use serde_json::{Value, json};
use std::{
    collections::HashMap,
    path::PathBuf,
    thread,
    time::{Duration, Instant},
};
use trestle_desktop::{
    protocol::{Client, Message},
    state::{Build, LogPage, Workspace},
};

struct Peer {
    client: Client,
    events: HashMap<String, u64>,
}

impl Peer {
    fn request(&mut self, method: &str, params: Value) -> Value {
        let id = self.client.send(method, params).unwrap();
        let until = Instant::now() + Duration::from_secs(20);
        loop {
            let mut response = None;
            for message in self.client.drain() {
                match message {
                    Message::Response {
                        id: response_id,
                        result,
                    } => {
                        assert_eq!(response_id, id);
                        response =
                            Some(result.unwrap_or_else(|error| panic!("{method}: {error:?}")));
                    }
                    Message::Event { event, .. } => {
                        let last = self.events.entry(event.build_id).or_default();
                        assert!(event.sequence > *last);
                        *last = event.sequence;
                    }
                    Message::Disconnected(reason) => panic!("{reason}"),
                }
            }
            if let Some(response) = response {
                return response;
            }
            assert!(Instant::now() < until, "{method} timed out");
            thread::sleep(Duration::from_millis(12));
        }
    }
}

#[test]
#[ignore = "requires TRESTLE_CORE and a clean copy in TRESTLE_TEST_WORKSPACE"]
fn native_workspace_round_trip() {
    let core = PathBuf::from(std::env::var_os("TRESTLE_CORE").expect("TRESTLE_CORE"));
    let path = std::env::var("TRESTLE_TEST_WORKSPACE").expect("TRESTLE_TEST_WORKSPACE");
    let mut peer = Peer {
        client: Client::start(&core).unwrap(),
        events: HashMap::new(),
    };
    assert_eq!(
        peer.request("system.handshake", json!({"versions":[1]}))["version"],
        1
    );
    let at = Instant::now();
    let workspace: Workspace =
        serde_json::from_value(peer.request("workspace.open", json!({"path":path}))).unwrap();
    assert_eq!(workspace.targets.len(), 3);
    assert!(
        workspace
            .targets
            .iter()
            .any(|target| target.id == "hello" && target.dependencies == ["greeter"])
    );
    eprintln!("Workspace open and target transfer: {:?}", at.elapsed());
    let start: Build = serde_json::from_value(peer.request(
        "build.start",
        json!({
            "workspace_id":workspace.id, "member":"cli", "targets":["hello"], "profile":"debug"
        }),
    ))
    .unwrap();
    let until = Instant::now() + Duration::from_secs(15);
    let build = loop {
        let status: Build = serde_json::from_value(
            peer.request("build.status", json!({"build_id":start.build_id})),
        )
        .unwrap();
        if !status.active() {
            break status;
        }
        assert!(Instant::now() < until);
        thread::sleep(Duration::from_millis(25));
    };
    assert_eq!(build.status, "completed");
    let page: LogPage = serde_json::from_value(
        peer.request("build.log", json!({"build_id":build.build_id,"offset":0})),
    )
    .unwrap();
    assert!(page.text.contains("link hello"), "{}", page.text);
    assert!(peer.events[&build.build_id] > 0);
    let analysis = peer.request("analysis.summary", json!({"build_id":build.build_id}));
    assert!(
        analysis["timings"]
            .as_array()
            .is_some_and(|timings| !timings.is_empty())
    );
    assert_eq!(analysis["cache"], Value::Null);
    peer.client.shutdown().unwrap();
    let mut reopened = Peer {
        client: Client::start(&core).unwrap(),
        events: HashMap::new(),
    };
    reopened.request("system.handshake", json!({"versions":[1]}));
    reopened.request("workspace.open", json!({"path":path}));
    let history = reopened.request("build.history", json!({"workspace_id":workspace.id}));
    assert!(
        history
            .as_array()
            .unwrap()
            .iter()
            .any(|entry| entry["build_id"] == build.build_id)
    );
    reopened.client.shutdown().unwrap();
}
