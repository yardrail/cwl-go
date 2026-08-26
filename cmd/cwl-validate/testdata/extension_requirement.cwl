cwlVersion: v1.2
class: CommandLineTool
$namespaces:
  ex: http://example.org/ext#
baseCommand: echo
requirements:
  - class: "ex:Magic"
    magic: 42
inputs: []
outputs: []
