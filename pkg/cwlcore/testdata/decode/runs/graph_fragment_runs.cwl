# A $graph whose entry point is addressed by fragment and runs a sibling.
#
# The workflow is deliberately not called "main", so the only way to reach it is
# "graph_fragment_runs.cwl#wf" -- which is how the conformance suite invokes a
# packed document, and the shape that used to defeat sibling linking.
cwlVersion: v1.2
$graph:
  - id: "#echo"
    class: CommandLineTool
    baseCommand: [echo]
    stdout: echoed.txt
    inputs:
      message:
        type: string
        inputBinding: {}
    outputs:
      out: stdout
  - id: "#wf"
    class: Workflow
    inputs:
      message: string
    outputs:
      out:
        type: File
        outputSource: speak/out
    steps:
      speak:
        run: "#echo"
        in:
          message: message
        out: [out]
