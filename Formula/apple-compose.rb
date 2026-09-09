class AppleCompose < Formula
  desc "docker compose for Apple's container runtime on macOS"
  homepage "https://github.com/skuirrels/apple-compose"
  url "https://github.com/skuirrels/apple-compose.git", tag: "v0.3.0"
  head "https://github.com/skuirrels/apple-compose.git", branch: "main"
  license "Apache-2.0"

  depends_on "go" => :build
  depends_on arch: :arm64
  depends_on :macos

  def install
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags, output: bin/"apple-compose"), "./cmd/apple-compose"
    system "go", "build", *std_go_args(ldflags: ldflags, output: bin/"apple-docker"), "./cmd/apple-docker"
    generate_completions_from_executable(bin/"apple-compose", "completion")
  end

  def caveats
    <<~EOS
      apple-compose needs Apple's `container` runtime (macOS 26 or later):
        https://github.com/apple/container/releases
      To use it as `container compose`, run:
        apple-compose plugin install
      apple-docker is a Docker CLI front end; alias it as docker:
        alias docker=apple-docker
    EOS
  end

  test do
    assert_match "apple-compose version", shell_output("#{bin}/apple-compose --version")
    assert_match "apple-docker version", shell_output("#{bin}/apple-docker --version")
  end
end
