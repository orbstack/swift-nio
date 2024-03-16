const PROGRAMS_CSV_PATH: &str = "/nix/orb/sys/.programs.csv";

fn read_packages_for_program(prog_name: &str) -> anyhow::Result<Vec<String>> {
    let csv_data = std::fs::read_to_string(PROGRAMS_CSV_PATH)?;
    let mut pkg_names = Vec::new();
    for line in csv_data.lines() {
        let mut iter = line.split(',');
        let name = iter.next().unwrap();
        let pkg_name = iter.next().unwrap();
        // add to vec
        if name == prog_name {
            pkg_names.push(pkg_name.to_string());
        }
    }
    Ok(pkg_names)
}

pub fn read_and_find_program(prog_name: &str) -> anyhow::Result<Option<String>> {
    let mut pkgs = read_packages_for_program(prog_name)?;

    // pick the best package with heuristics

    // 1. direct match?
    if let Some(pkg) = pkgs.iter().find(|p| *p == prog_name) {
        return Ok(Some(pkg.to_string()));
    }

    // then sort by comparator:
    // 1. (bool) whether pkg name starts with prog_name (true = first)
    // 2. (int) length of pkg name (shortest first)
    // 3. alphanumeric (e.g. python312 > python310)
    // examples:
    //   - sorted for node: ["nodejs_21", "nodejs_20", "nodejs_18", "nodejs-slim", "nodejs-slim_21", "nodejs-slim_18", "graalvmCEPackages.graalnodejs"]
    //   - sorted for python: ["python3", "python39", "python313", "python312", "python310", "python3Full", "python39Full", "python313Full", "python312Full", "python310Full", "python3Minimal", "qtile", "jupyter", "jupyter-all", "graalvmCEPackages.graalpy"]
    pkgs.sort_by(|a, b| {
        let a_starts = a.starts_with(prog_name);
        let b_starts = b.starts_with(prog_name);
        if a_starts != b_starts {
            b_starts.cmp(&a_starts)
        } else if a.len() != b.len() {
            a.len().cmp(&b.len())
        } else {
            b.cmp(a)
        }
    });

    Ok(pkgs.first().map(|p| p.to_string()))
}
