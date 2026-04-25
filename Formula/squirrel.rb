class Squirrel < Formula
  desc "Database-aware backup tool for PostgreSQL and MySQL with content-addressed storage"
  homepage "https://github.com/elpol4k0/squirrel"
  url "https://github.com/elpol4k0/squirrel/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"
  head "https://github.com/elpol4k0/squirrel.git", branch: "develop"

  depends_on "go" => :build

  def install
    ldflags = %W[
      -s -w
      -X main.version=#{version}
    ]
    system "go", "build", *std_go_args(ldflags:), "./cmd/squirrel"

    generate_completions_from_executable(bin/"squirrel", "completion")
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/squirrel version")
    assert_match "squirrel", shell_output("#{bin}/squirrel --help")
  end
end
