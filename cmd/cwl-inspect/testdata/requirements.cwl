cwlVersion: v1.2
class: CommandLineTool
id: "#loaded"
$namespaces:
  ex: http://example.org/ext#
baseCommand: echo
requirements:
  - class: SoftwareRequirement
    packages:
      - package: samtools
        version: ["1.19"]
        specs: ["https://identifiers.org/rrid/RRID:SCR_002105"]
  - class: EnvVarRequirement
    envDef:
      - envName: PATH
        envValue: /usr/bin
  - class: InitialWorkDirRequirement
    listing:
      - entryname: script.sh
        entry: "echo hi"
  - class: SchemaDefRequirement
    types:
      - name: "#rec"
        type: record
        fields:
          - name: "#rec/a"
            type: string
  - class: NetworkAccess
    networkAccess: true
  - class: WorkReuse
    enableReuse: false
  - class: InplaceUpdateRequirement
    inplaceUpdate: true
  - class: LoadListingRequirement
    loadListing: no_listing
  - class: ToolTimeLimit
    timelimit: 30
  - class: ShellCommandRequirement
hints:
  - class: "ex:Note"
    level: info
inputs: []
outputs: []
