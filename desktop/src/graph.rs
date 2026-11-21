use crate::state::Target;
use std::collections::{HashMap, HashSet, VecDeque};

#[derive(Clone, Copy, Debug)]
pub struct Node {
    pub layer: usize,
    pub row: usize,
}

#[derive(Debug)]
pub struct Graph {
    pub nodes: Vec<Node>,
    pub edges: Vec<(usize, usize)>,
    pub layers: Vec<Vec<usize>>,
    pub rows: usize,
    dependencies: Vec<Vec<usize>>,
    dependents: Vec<Vec<usize>>,
}

impl Graph {
    pub fn layout(targets: &[Target]) -> Result<Self, String> {
        let mut ids = HashMap::with_capacity(targets.len());
        for (i, target) in targets.iter().enumerate() {
            if ids.insert(target.id.as_str(), i).is_some() {
                return Err(format!("Duplicate target {}", target.id));
            }
        }
        let mut edges = Vec::new();
        let mut dependencies = vec![Vec::new(); targets.len()];
        let mut dependents = vec![Vec::new(); targets.len()];
        for (i, target) in targets.iter().enumerate() {
            for dependency in &target.dependencies {
                let j = *ids
                    .get(dependency.as_str())
                    .ok_or_else(|| format!("Missing dependency {dependency} for {}", target.id))?;
                dependencies[i].push(j);
                dependents[j].push(i);
                edges.push((j, i));
            }
        }
        let mut remaining: Vec<_> = dependencies.iter().map(Vec::len).collect();
        let mut pending: VecDeque<_> = (0..targets.len()).filter(|&i| remaining[i] == 0).collect();
        let mut nodes = vec![Node { layer: 0, row: 0 }; targets.len()];
        let mut layers: Vec<Vec<usize>> = Vec::new();
        let mut visited = 0;
        let mut rows = 0;
        while let Some(i) = pending.pop_front() {
            visited += 1;
            let layer = nodes[i].layer;
            if layer >= layers.len() {
                layers.resize_with(layer + 1, Vec::new);
            }
            nodes[i].row = layers[layer].len();
            layers[layer].push(i);
            rows = rows.max(layers[layer].len());
            for &consumer in &dependents[i] {
                nodes[consumer].layer = nodes[consumer].layer.max(layer + 1);
                remaining[consumer] -= 1;
                if remaining[consumer] == 0 {
                    pending.push_back(consumer);
                }
            }
        }
        if visited != targets.len() {
            return Err("The Core graph contains a dependency cycle".into());
        }
        Ok(Self {
            nodes,
            edges,
            layers,
            rows,
            dependencies,
            dependents,
        })
    }

    pub fn related(&self, node: usize, reverse: bool, transitive: bool) -> HashSet<usize> {
        let edges = if reverse {
            &self.dependents
        } else {
            &self.dependencies
        };
        let Some(neighbors) = edges.get(node) else {
            return HashSet::new();
        };
        let mut found = HashSet::new();
        let mut pending = neighbors.clone();
        while let Some(i) = pending.pop() {
            if found.insert(i) && transitive {
                pending.extend_from_slice(&edges[i]);
            }
        }
        found
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn target(id: usize, deps: Vec<String>) -> Target {
        serde_json::from_value(serde_json::json!({
            "id": id.to_string(), "member":"", "type":"static", "compiler":"auto", "dependencies":deps,
        })).unwrap()
    }

    #[test]
    fn orders_layers_and_detects_cycles() {
        let targets = [
            target(0, vec![]),
            target(1, vec!["0".into()]),
            target(2, vec!["1".into()]),
        ];
        let graph = Graph::layout(&targets).unwrap();
        assert_eq!(graph.nodes[2].layer, 2);
        assert_eq!(graph.related(2, false, true), HashSet::from([0, 1]));
        assert_eq!(graph.related(0, true, false), HashSet::from([1]));
        assert!(
            Graph::layout(&[target(0, vec!["1".into()]), target(1, vec!["0".into()])]).is_err()
        );
    }

    #[test]
    fn scale_samples() {
        for count in [100, 1000, 10000] {
            let begin = std::time::Instant::now();
            let targets: Vec<_> = (0..count)
                .map(|i| {
                    target(
                        i,
                        if i >= 10 {
                            vec![(i - 10).to_string()]
                        } else {
                            vec![]
                        },
                    )
                })
                .collect();
            let layout_at = std::time::Instant::now();
            let graph = Graph::layout(&targets).unwrap();
            let interact_at = std::time::Instant::now();
            assert_eq!(graph.related(count - 1, false, true).len(), count / 10 - 1);
            eprintln!(
                "{count} nodes: prepare {:?}, layout {:?}, highlight {:?}",
                layout_at - begin,
                interact_at - layout_at,
                interact_at.elapsed()
            );
        }
    }
}
