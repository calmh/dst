/*

Package udt implements "UDT: UDP-based Data Transfer Protocol" as defined in
draft-gg-udt-03.txt.

Or that's the plan; currently it implements a small strict subset of proper
UDT.

UDT is a way to get reliable stream connections (like TCP) on top of UDP. The
advantages include better performance over high latency or otherwise
unreliable networks and easier NAT penetration.

*/
package udt
