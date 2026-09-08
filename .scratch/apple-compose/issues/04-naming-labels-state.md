# 04 Naming, labels and tool state
Type: grilling
Status: resolved

## Question
What names, labels and on-disk state does the tool own?

## Answer
Containers `<project>-<service>-<n>`, networks `<project>_<network>`, volumes `<project>_<volume>`, exactly as Docker Compose. Labels `com.docker.compose.project`, `.service`, `.container-number`, `.oneoff`, `.config-hash`, `.project.working_dir`, `.project.config_files`, `.network`, `.volume`. The runtime is the source of truth for resources; the only tool-owned state is the generated hosts files under `~/Library/Application Support/apple-compose/projects/<project>/hosts/`.
