Feature: material-flow-control acceptance criteria (warehouse-docs docs/wcs/implementation-handoff.md)
  The WES asks for a transport over REST and learns its progress from events; the subsystems are the memory
  fakes and their facts are fed to the use cases the way the Kafka consumers feed them.

  Background:
    Given the example material-flow layout

  Scenario: Goods-to-person, then the return writes no stock fact
    Given container "TOTE-1" is stored in the AS/RS
    When the WES requests "TOTE-1" from "WH1-AUTO-ASRS-ASRS1-00-00-S" to "ST1" for "PICK" as "rq-1"
    And the AS/RS presents "TOTE-1" at port "OUT1" with scan "READ"
    And a robot carries "TOTE-1" to "ST1"
    Then "ContainerArrivedAtStation" is published for station "ST1" at queue position 1 for "TOTE-1" of "rq-1"
    And transport "rq-1" is "COMPLETED"
    When the WES requests "TOTE-1" from "ST1" to "WH1-AUTO-ASRS-ASRS1-00-00-S" for "RETURN" as "rq-2"
    And a robot carries "TOTE-1" to "ASRS1-IN1"
    And the AS/RS stores "TOTE-1"
    Then transport "rq-2" is "COMPLETED"
    And the events of "rq-2" are "TransportAccepted,TransportCompleted"
    And container "TOTE-1" is "IN_ASRS" at "WH1-AUTO-ASRS-ASRS1-00-00-S"
    And no event names a robot, a node, a rack position or a leg

  Scenario: Putaway into the AS/RS
    When the WES requests "TOTE-2" from "WH1-RCV-DEC1" to "WH1-AUTO-ASRS-ASRS1-00-00-S" for "PUTAWAY" as "rq-3"
    And a robot carries "TOTE-2" to "ASRS1-IN1"
    And the AS/RS stores "TOTE-2"
    Then the events of "rq-3" are "TransportAccepted,TransportCompleted"
    And container "TOTE-2" is "IN_ASRS" at "WH1-AUTO-ASRS-ASRS1-00-00-S"

  Scenario: Replenishment to the forward handoff, then the return
    Given container "TOTE-3" is stored in the AS/RS
    When the WES requests "TOTE-3" from "WH1-AUTO-ASRS-ASRS1-00-00-S" to "WH1-FWD-FWD1" for "REPLENISH" as "rq-4"
    And the AS/RS presents "TOTE-3" at port "OUT1" with scan "READ"
    And a robot carries "TOTE-3" to "FWD1"
    Then transport "rq-4" is "COMPLETED"
    And container "TOTE-3" is "AT_HANDOFF" at "WH1-FWD-FWD1"
    When the WES requests "TOTE-3" from "WH1-FWD-FWD1" to "WH1-AUTO-ASRS-ASRS1-00-00-S" for "RETURN" as "rq-5"
    And a robot carries "TOTE-3" to "ASRS1-IN1"
    And the AS/RS stores "TOTE-3"
    Then the events of "rq-5" are "TransportAccepted,TransportCompleted"
    And container "TOTE-3" is "IN_ASRS" at "WH1-AUTO-ASRS-ASRS1-00-00-S"

  Scenario: Outbound to a pack station
    When the WES requests "O-1" from "ST1" to "WH1-PCK-PK1" for "OUTBOUND" as "rq-6"
    And a robot carries "O-1" to "PK1"
    Then the events of "rq-6" are "TransportAccepted,TransportCompleted"
    And container "O-1" is "AT_HANDOFF" at "WH1-PCK-PK1"

  Scenario: A second request for a busy container is rejected
    Given container "TOTE-4" is stored in the AS/RS
    When the WES requests "TOTE-4" from "WH1-AUTO-ASRS-ASRS1-00-00-S" to "ST1" for "PICK" as "rq-7"
    And the WES requests "TOTE-4" from "WH1-AUTO-ASRS-ASRS1-00-00-S" to "ST1" for "PICK" as "rq-8"
    Then request "rq-8" is rejected "CONTAINER_BUSY"
    And 1 transport order exists for "TOTE-4"

  Scenario: A retried request returns the original outcome
    Given container "TOTE-5" is stored in the AS/RS
    When the WES requests "TOTE-5" from "WH1-AUTO-ASRS-ASRS1-00-00-S" to "ST1" for "PICK" as "rq-9"
    And the WES requests "TOTE-5" from "WH1-AUTO-ASRS-ASRS1-00-00-S" to "ST1" for "PICK" as "rq-9"
    Then both answers name the same transport order
    And 1 transport order exists for "TOTE-5"

  Scenario: A no-read at a port sends the container to clarification
    Given container "TOTE-6" is stored in the AS/RS
    When the WES requests "TOTE-6" from "WH1-AUTO-ASRS-ASRS1-00-00-S" to "ST1" for "PICK" as "rq-10"
    And the AS/RS presents "TOTE-6" at port "OUT1" with scan "NO_READ"
    Then the events of "rq-10" are "TransportAccepted,ContainerSentToClarification,TransportFailed"
    And "TOTE-6" of "rq-10" is sent to clarification "NO_READ" at "WH1-CLR-CLR1", detected at "WH1-AUTO-ASRS-ASRS1-01-00-OUT1"
    And the published TransportFailed of "rq-10" has reason "CLARIFICATION"
    And transport "rq-10" failed "CLARIFICATION"
    When a robot carries "TOTE-6" to "CLR1"
    Then container "TOTE-6" is "AT_CLARIFICATION" at "WH1-CLR-CLR1"

  Scenario: Half the robots go offline and the capacity reflects it
    Given the AMR fleet reports 4 robots, 4 online, 4 available
    When the AMR fleet reports 4 robots, 2 online, 2 available
    Then the last "AMR_FLEET" capacity is "DEGRADED" with 2 available
