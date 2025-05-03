package main

import (
   "bufio"
   "bytes"
   "embed"
   "fmt"
   "log"
   "os"
   "os/exec"
   "path/filepath"
   "strings"

   "golang.org/x/term"
)

func main() {
   if len(os.Args) > 1 {
       switch os.Args[1] {
       case "install":
           if err := install(); err != nil {
               log.Fatalf("install failed: %v", err)
           }
           return
       case "-h", "--help", "help":
           usage()
           return
       }
   }
   if err := runCli(os.Args[1:]); err != nil {
       log.Fatalf("error: %v", err)
   }
}

func usage() {
   prog := filepath.Base(os.Args[0])
   fmt.Printf(`Usage: %s [DIR1 DIR2 ...]

Mounts each DIRi at /workspace/<basename(DIRi)> in the claudex container.
If no DIR is provided, mounts the current directory at /workspace.
Examples:
  %s
  %s service1/ service2/

Install or update the Docker image:
  %s install
`, prog, prog, prog, prog)
   os.Exit(0)
}

//go:embed Dockerfile init-firewall.sh
var dockerContextFS embed.FS

// prepareBuildContext writes embedded Dockerfile and init-firewall.sh to a temp directory.
func prepareBuildContext() (string, error) {
   tmpDir, err := os.MkdirTemp("", "claudex-build-")
   if err != nil {
       return "", fmt.Errorf("cannot create temp build dir: %w", err)
   }
   files := []string{"Dockerfile", "init-firewall.sh"}
   for _, name := range files {
       data, err := dockerContextFS.ReadFile(name)
       if err != nil {
           os.RemoveAll(tmpDir)
           return "", fmt.Errorf("cannot read embedded %s: %w", name, err)
       }
       outPath := filepath.Join(tmpDir, name)
       if err := os.WriteFile(outPath, data, 0644); err != nil {
           os.RemoveAll(tmpDir)
           return "", fmt.Errorf("cannot write %s to temp dir: %w", name, err)
       }
   }
   return tmpDir, nil
}

// install builds or updates the claudex Docker image.
func install() error {
   fmt.Println("Building/updating the claudex container image...")
   ctxDir, err := prepareBuildContext()
   if err != nil {
       return err
   }
   defer os.RemoveAll(ctxDir)
   cmd := exec.Command("docker", "build", "-t", "claudex", ctxDir)
   cmd.Stdout = os.Stdout
   cmd.Stderr = os.Stderr
   if err := cmd.Run(); err != nil {
       return fmt.Errorf("docker build failed: %w", err)
   }
   return nil
}

// runCli handles the container setup and shell attachment.
func runCli(args []string) error {
   home, err := os.UserHomeDir()
   if err != nil {
       return err
   }

   var mounts []string
   // Mount Claude credentials if available
   claudeJson := filepath.Join(home, ".claude.json")
   if _, err := os.Stat(claudeJson); err == nil {
       mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/node/.claude.json", claudeJson))
   } else {
       fmt.Fprintf(os.Stderr, "Warning: %s not found; proceeding without it.\n", claudeJson)
   }

   // Mount workspace directories
   if len(args) == 0 {
       cwd, err := os.Getwd()
       if err != nil {
           return err
       }
       abs, err := filepath.Abs(cwd)
       if err != nil {
           return fmt.Errorf("invalid path: %s", cwd)
       }
       fi, err := os.Stat(abs)
       if err != nil || !fi.IsDir() {
           return fmt.Errorf("'%s' is not a directory", abs)
       }
       mounts = append(mounts, "-v", fmt.Sprintf("%s:/workspace", abs))
   } else {
       for _, d := range args {
           abs, err := filepath.Abs(d)
           if err != nil {
               return fmt.Errorf("invalid path: %s", d)
           }
           fi, err := os.Stat(abs)
           if err != nil || !fi.IsDir() {
               return fmt.Errorf("'%s' is not a directory", abs)
           }
           name := filepath.Base(abs)
           mounts = append(mounts, "-v", fmt.Sprintf("%s:/workspace/%s", abs, name))
       }
   }

   // Prompt to mount instructions if interactive
   if term.IsTerminal(int(os.Stdin.Fd())) {
       fmt.Print("Do you have an instructions file or directory to mount for this session? [y/N] ")
       reader := bufio.NewReader(os.Stdin)
       ans, _ := reader.ReadString('\n')
       ans = strings.TrimSpace(ans)
       if strings.EqualFold(ans, "y") {
           fmt.Print("Enter path to instructions file or directory: ")
           instr, _ := reader.ReadString('\n')
           instr = strings.TrimSpace(instr)
           abs, err := filepath.Abs(instr)
           if err != nil {
               return fmt.Errorf("invalid path: %s", instr)
           }
           fi, err := os.Stat(abs)
           if err != nil {
               return fmt.Errorf("'%s' does not exist", abs)
           }
           if fi.IsDir() {
               mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/node/instructions", abs))
               fmt.Printf("Mounted instructions directory: %s -> /home/node/instructions\n", abs)
           } else {
               name := filepath.Base(abs)
               mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/node/instructions/%s", abs, name))
               fmt.Printf("Mounted instructions file: %s -> /home/node/instructions/%s\n", abs, name)
           }
       }
   }

   // Ensure the 'claudex' image exists; build if missing
   out, err := exec.Command("docker", "images", "-q", "claudex").Output()
   if err != nil {
       return fmt.Errorf("docker images check failed: %w", err)
   }
   if len(bytes.TrimSpace(out)) == 0 {
       fmt.Println("Building 'claudex' container image...")
       ctxDir, err := prepareBuildContext()
       if err != nil {
           return err
       }
       defer os.RemoveAll(ctxDir)
       cmd := exec.Command("docker", "build", "-t", "claudex", ctxDir)
       cmd.Stdout = os.Stdout
       cmd.Stderr = os.Stderr
       if err := cmd.Run(); err != nil {
           return fmt.Errorf("docker build failed: %w", err)
       }
   } else {
       fmt.Println("'claudex' container image already exists.")
   }

   // Remove any existing container named 'claudex'
   exec.Command("docker", "rm", "-f", "claudex").Run()

   // Run the container in detached mode
   runArgs := []string{"run", "--name", "claudex", "-d", "-e", "OPENAI_API_KEY", "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW"}
   runArgs = append(runArgs, mounts...)
   runArgs = append(runArgs, "claudex", "sleep", "infinity")
   cmdRun := exec.Command("docker", runArgs...)
   cmdRun.Stdout = os.Stdout
   cmdRun.Stderr = os.Stderr
   if err := cmdRun.Run(); err != nil {
       return fmt.Errorf("docker run failed: %w", err)
   }

   // Initialize Git repository if not present, using 'main' as the initial branch
   gitCmd := "if [ ! -d /workspace/.git ]; then git -C /workspace init -b main && git -C /workspace config user.name 'Claudex CLI' && git -C /workspace config user.email 'claudex@local' && git -C /workspace add . && git -C /workspace commit -m 'Initial workspace commit'; fi"
   cmdGit := exec.Command("docker", "exec", "-u", "node", "claudex", "bash", "-lc", gitCmd)
   cmdGit.Stdout = os.Stdout
   cmdGit.Stderr = os.Stderr
   if err := cmdGit.Run(); err != nil {
       return fmt.Errorf("git init failed: %w", err)
   }

   // Initialize the firewall inside the container
   cmdFirewall := exec.Command("docker", "exec", "claudex", "bash", "-c", "sudo /usr/local/bin/init-firewall.sh")
   cmdFirewall.Stdout = os.Stdout
   cmdFirewall.Stderr = os.Stderr
   if err := cmdFirewall.Run(); err != nil {
       return fmt.Errorf("init-firewall failed: %w", err)
   }

   // Attach an interactive shell
   cmdShell := exec.Command("docker", "exec", "-it", "claudex", "bash")
   cmdShell.Stdin = os.Stdin
   cmdShell.Stdout = os.Stdout
   cmdShell.Stderr = os.Stderr
   return cmdShell.Run()
}