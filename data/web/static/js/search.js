function search(url, before, after) {
    let search = document.getElementById("search").value;

    const params = new URLSearchParams({
        search: search,
    });
    if (before && before !== "") {
        params.set("before", before);
    }
    if (after && after !== "") {
        params.set("after", after);
    }


    let colls = document.getElementsByClassName("collectionButton")
    let collParam = "";
    for (let i = 0; i < colls.length; i++) {
        if (colls[i].getAttribute("selected") === "true") {
            collParam += colls[i].getAttribute("value") + ",";
        }
    }
    if ( colls.length > 0 ){
        params.set("collections", collParam);
    }
/*
    let colls = document.getElementsByClassName("collectionCheck")
    let collParam = "";
    for (let i = 0; i < colls.length; i++) {
        if (colls[i].checked) {
            collParam += colls[i].value + ",";
        }
    }
    if ( colls.length > 0 ){
        params.set("collections", collParam);
    }
*/
    window.location.href = url + "?" + params.toString();
}