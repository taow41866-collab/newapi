"""Run Windows-cross-compiled Linux Go test binaries in the existing WSL host.

Pass to go test -exec as: E:/python/python.exe <absolute path to this file>.
This changes no source or environment settings and preserves the test exit code.
"""
import os
import pathlib
import subprocess
import sys


def mounted_path(value):
    path = pathlib.PureWindowsPath(os.path.abspath(value))
    if not path.drive or len(path.drive) != 2:
        raise ValueError("Expected a local Windows drive path")
    return "/mnt/" + path.drive[0].lower() + "/" + "/".join(path.parts[1:])


if __name__ == "__main__":
    command = ["wsl.exe", "-d", "Ubuntu-2404", "--cd", mounted_path(os.getcwd()),
               "--exec", mounted_path(sys.argv[1]), *sys.argv[2:]]
    raise SystemExit(subprocess.call(command))
